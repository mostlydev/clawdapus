// Command runner-adoption-snapshot collects the adoption evidence behind
// ADR-026 and writes a dated JSON artifact under docs/evidence/.
//
// The primary metric is new forks bucketed into fixed-width windows. Unlike
// stars, total downloads, and fork counts, a bucketed fork rate is not
// cumulative, so it can fall — which is what makes it usable as an adoption
// time series. See ADR-026 for why the two obvious alternatives were rejected:
// stargazer timestamps are unavailable (the endpoint 404s for external repos)
// and per-release downloads-per-day is confounded by post-publication decay.
//
// Classification thresholds deliberately live in ADR-026, not here, so a future
// audit can refresh the data without inheriting today's judgement.
//
// Usage:
//
//	go run ./scripts/runner-adoption-snapshot -out docs/evidence
//
// Requires an authenticated `gh` CLI on PATH.
//
// Fork pagination dominates the runtime: the window tally reads 100 forks per
// request until it passes the oldest window, so a runner with tens of thousands
// of recent forks costs hundreds of sequential API calls. A full 4x30d snapshot
// across all runners spends a few thousand requests against the 5000/hour core
// limit. Narrow -windows or -window-days when iterating.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// runners maps each in-tree driver to its canonical upstream repository.
var runners = []runner{
	{Driver: "openclaw", Repo: "openclaw/openclaw"},
	{Driver: "hermes", Repo: "NousResearch/hermes-agent"},
	// PyPI publishes no project URLs for nanobot-ai; the repository below was
	// confirmed by matching its pyproject.toml name and version to the package.
	{Driver: "nanobot", Repo: "HKUDS/nanobot", PyPI: "nanobot-ai"},
	{Driver: "nanoclaw", Repo: "nanocoai/nanoclaw", Note: "canonical repo redirected from qwibitai/nanoclaw"},
	{Driver: "picoclaw", Repo: "sipeed/picoclaw", DockerHub: "sipeed/picoclaw"},
	{Driver: "microclaw", Repo: "microclaw/microclaw"},
	{Driver: "nullclaw", Repo: "nullclaw/nullclaw"},
}

type runner struct {
	Driver    string `json:"driver"`
	Repo      string `json:"repo,omitempty"`
	PyPI      string `json:"pypi,omitempty"`
	DockerHub string `json:"docker_hub,omitempty"`
	Note      string `json:"note,omitempty"`
}

type snapshot struct {
	CapturedAt  time.Time      `json:"captured_at"`
	WindowDays  int            `json:"window_days"`
	WindowCount int            `json:"window_count"`
	Limitations []string       `json:"limitations"`
	Runners     []runnerResult `json:"runners"`
}

type runnerResult struct {
	runner
	Stars         int      `json:"stars,omitempty"`
	Forks         int      `json:"forks,omitempty"`
	Archived      bool     `json:"archived,omitempty"`
	PushedAt      string   `json:"pushed_at,omitempty"`
	LatestRelease string   `json:"latest_release,omitempty"`
	ForkWindows   []int    `json:"fork_windows,omitempty"`
	PyPIMonthly   []bucket `json:"pypi_monthly,omitempty"`
	Errors        []string `json:"errors,omitempty"`
	// ForkWindowsTruncated marks counts that hit the page cap before reaching
	// the oldest window. Such counts are lower bounds, not totals.
	ForkWindowsTruncated bool `json:"fork_windows_truncated,omitempty"`
}

type bucket struct {
	Period string `json:"period"`
	Count  int    `json:"count"`
}

func main() {
	out := flag.String("out", "docs/evidence", "directory for the dated JSON artifact")
	windowDays := flag.Int("window-days", 30, "width of each fork-count window in days")
	windows := flag.Int("windows", 4, "number of consecutive windows to collect, newest first")
	maxPages := flag.Int("max-pages", 0, "cap fork pages read per runner (0 = no cap); capped counts are marked truncated")
	flag.Parse()

	if err := run(*out, *windowDays, *windows, *maxPages); err != nil {
		fmt.Fprintln(os.Stderr, "runner-adoption-snapshot:", err)
		os.Exit(1)
	}
}

func run(outDir string, windowDays, windowCount, maxPages int) error {
	now := time.Now().UTC().Truncate(24 * time.Hour)
	snap := snapshot{
		CapturedAt:  now,
		WindowDays:  windowDays,
		WindowCount: windowCount,
		Limitations: []string{
			"GitHub /stargazers with the star+json media type returns 404 for external repositories from this environment; it is not rate limiting and not a token scope issue. Stargazer time series are unavailable.",
			"Per-release downloads normalized by release age is confounded by post-publication download decay and must not be read as a growth trend.",
			"ghcr.io publishes no public pull counts, so image pulls are only comparable for docker.io-hosted runners.",
			"No metric here measures Clawdapus-side usage. All of it is upstream popularity.",
		},
	}

	for _, r := range runners {
		result := runnerResult{runner: r}
		if r.Repo != "" {
			if err := collectRepo(&result, now, windowDays, windowCount, maxPages); err != nil {
				result.Errors = append(result.Errors, err.Error())
			}
		}
		if r.PyPI != "" {
			monthly, err := collectPyPI(r.PyPI)
			if err != nil {
				result.Errors = append(result.Errors, err.Error())
			}
			result.PyPIMonthly = monthly
		}
		snap.Runners = append(snap.Runners, result)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", outDir, err)
	}
	path := filepath.Join(outDir, now.Format("2006-01-02")+"-runner-adoption.json")
	body, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	printTable(snap)
	fmt.Fprintf(os.Stderr, "\nwrote %s\n", path)
	return nil
}

func collectRepo(result *runnerResult, now time.Time, windowDays, windowCount, maxPages int) error {
	meta, err := ghJSON("repos/" + result.Repo)
	if err != nil {
		return fmt.Errorf("repo metadata: %w", err)
	}
	result.Stars = intField(meta, "stargazers_count")
	result.Forks = intField(meta, "forks_count")
	result.Archived = boolField(meta, "archived")
	result.PushedAt = stringField(meta, "pushed_at")

	if releases, err := ghArray("repos/" + result.Repo + "/releases?per_page=1"); err == nil && len(releases) > 0 {
		result.LatestRelease = stringField(releases[0], "published_at")
	}

	counts, truncated, err := forkWindows(result.Repo, now, windowDays, windowCount, maxPages)
	if err != nil {
		return fmt.Errorf("fork windows: %w", err)
	}
	result.ForkWindows = counts
	result.ForkWindowsTruncated = truncated
	return nil
}

// forkWindows pages newest-first through forks and tallies each one into the
// window its creation timestamp falls in, stopping as soon as it reads past the
// oldest window. Repositories with very large fork counts make this the most
// expensive call in the collector, which is why it terminates early rather than
// paginating the whole list.
func forkWindows(repo string, now time.Time, windowDays, windowCount, maxPages int) ([]int, bool, error) {
	counts := make([]int, windowCount)
	horizon := now.AddDate(0, 0, -windowDays*windowCount)

	for page := 1; ; page++ {
		if maxPages > 0 && page > maxPages {
			return counts, true, nil
		}
		path := fmt.Sprintf("repos/%s/forks?sort=newest&per_page=100&page=%d", repo, page)
		items, err := ghArray(path)
		if err != nil {
			return nil, false, err
		}
		if len(items) == 0 {
			return counts, false, nil
		}
		for _, item := range items {
			created, err := time.Parse(time.RFC3339, stringField(item, "created_at"))
			if err != nil {
				continue
			}
			if created.Before(horizon) {
				// Forks are newest-first, so everything after this is older.
				return counts, false, nil
			}
			if index, ok := windowIndex(now, created, windowDays, windowCount); ok {
				counts[index]++
			}
		}
	}
}

// collectPyPI returns monthly download totals excluding mirrors. This is the
// only genuine, non-cumulative adoption time series available for any runner,
// and it exists only for runners distributed as a Python package.
func collectPyPI(pkg string) ([]bucket, error) {
	body, err := httpGet("https://pypistats.org/api/packages/" + pkg + "/overall")
	if err != nil {
		return nil, fmt.Errorf("pypistats %s: %w", pkg, err)
	}
	var payload struct {
		Data []struct {
			Category  string `json:"category"`
			Date      string `json:"date"`
			Downloads int    `json:"downloads"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("pypistats %s: %w", pkg, err)
	}
	totals := map[string]int{}
	for _, row := range payload.Data {
		if row.Category != "without_mirrors" || len(row.Date) < 7 {
			continue
		}
		totals[row.Date[:7]] += row.Downloads
	}
	months := make([]string, 0, len(totals))
	for month := range totals {
		months = append(months, month)
	}
	sort.Strings(months)
	out := make([]bucket, 0, len(months))
	for _, month := range months {
		out = append(out, bucket{Period: month, Count: totals[month]})
	}
	return out, nil
}

func httpGet(url string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// windowIndex places a fork creation time into a window counted back from now,
// where window 0 is the most recent. Times in the future, or older than the
// oldest window, are not counted.
func windowIndex(now, created time.Time, windowDays, windowCount int) (int, bool) {
	if windowDays <= 0 || windowCount <= 0 || created.After(now) {
		return 0, false
	}
	index := int(now.Sub(created).Hours() / 24 / float64(windowDays))
	if index < 0 || index >= windowCount {
		return 0, false
	}
	return index, true
}

func printTable(snap snapshot) {
	fmt.Printf("Runner adoption snapshot %s (%d x %dd windows, newest first)\n\n",
		snap.CapturedAt.Format("2006-01-02"), snap.WindowCount, snap.WindowDays)
	fmt.Printf("%-11s %10s %8s  %s\n", "runner", "stars", "ret", "fork windows")
	sorted := append([]runnerResult(nil), snap.Runners...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Stars > sorted[j].Stars })
	for _, r := range sorted {
		retention := "-"
		if n := len(r.ForkWindows); n > 1 && r.ForkWindows[n-1] > 0 {
			retention = fmt.Sprintf("%.2f", float64(r.ForkWindows[0])/float64(r.ForkWindows[n-1]))
		}
		cells := make([]string, 0, len(r.ForkWindows))
		for _, c := range r.ForkWindows {
			cells = append(cells, fmt.Sprint(c))
		}
		fmt.Printf("%-11s %10d %8s  %s\n", r.Driver, r.Stars, retention, strings.Join(cells, " / "))
		for _, e := range r.Errors {
			fmt.Printf("%-11s %s\n", "", "error: "+e)
		}
	}
}

func ghJSON(path string) (map[string]any, error) {
	body, err := gh(path)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func ghArray(path string) ([]map[string]any, error) {
	body, err := gh(path)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// gh calls the GitHub REST API directly, borrowing the `gh` CLI's credentials
// once rather than spawning a subprocess per request. Fork pagination issues
// hundreds of calls for a large repo, and per-call process startup dominated
// the runtime when this shelled out to `gh api` each time.
func gh(path string) ([]byte, error) {
	token, err := githubToken()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/"+strings.TrimPrefix(path, "/"), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := githubClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

var (
	githubClient    = &http.Client{Timeout: 30 * time.Second}
	githubTokenOnce sync.Once
	githubTokenVal  string
	githubTokenErr  error
)

func githubToken() (string, error) {
	githubTokenOnce.Do(func() {
		if env := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); env != "" {
			githubTokenVal = env
			return
		}
		out, err := exec.Command("gh", "auth", "token").Output()
		if err != nil {
			githubTokenErr = fmt.Errorf("no GITHUB_TOKEN set and `gh auth token` failed: %w", err)
			return
		}
		githubTokenVal = strings.TrimSpace(string(out))
	})
	return githubTokenVal, githubTokenErr
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func intField(m map[string]any, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return 0
}

func boolField(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}
