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
// Classification judgments deliberately live in ADR-026, not here, so a future
// audit can refresh the data without inheriting today's judgment.
//
// Usage:
//
//	go run ./scripts/runner-adoption-snapshot -out docs/evidence
//
// Requires an authenticated `gh` CLI on PATH. Fork-window boundaries are found
// with binary search over newest-first pages and cached across windows, so even
// large repositories take tens of requests rather than hundreds.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	Stars          int      `json:"stars,omitempty"`
	Forks          int      `json:"forks,omitempty"`
	Archived       bool     `json:"archived,omitempty"`
	PushedAt       string   `json:"pushed_at,omitempty"`
	LatestRelease  string   `json:"latest_release,omitempty"`
	ForkWindows    []int    `json:"fork_windows,omitempty"`
	CommitWindows  []int    `json:"commit_windows_90d,omitempty"`
	ReleaseCount   int      `json:"release_count,omitempty"`
	AssetDownloads int      `json:"release_asset_downloads,omitempty"`
	DockerHubPulls int      `json:"docker_hub_pulls,omitempty"`
	PyPIMonthly    []bucket `json:"pypi_monthly,omitempty"`
	Errors         []string `json:"errors,omitempty"`
}

type bucket struct {
	Period string `json:"period"`
	Count  int    `json:"count"`
}

func main() {
	out := flag.String("out", "docs/evidence", "directory for the dated JSON artifact")
	windowDays := flag.Int("window-days", 30, "width of each fork-count window in days")
	windows := flag.Int("windows", 4, "number of consecutive windows to collect, newest first")
	flag.Parse()

	if err := run(*out, *windowDays, *windows); err != nil {
		fmt.Fprintln(os.Stderr, "runner-adoption-snapshot:", err)
		os.Exit(1)
	}
}

func run(outDir string, windowDays, windowCount int) error {
	now := time.Now().UTC()
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

	results := make([]runnerResult, len(runners))
	var wg sync.WaitGroup
	for index, r := range runners {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := runnerResult{runner: r}
			if r.Repo != "" {
				if err := collectRepo(&result, now, windowDays, windowCount); err != nil {
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
			if r.DockerHub != "" {
				pulls, err := collectDockerHubPulls(r.DockerHub)
				if err != nil {
					result.Errors = append(result.Errors, err.Error())
				} else {
					result.DockerHubPulls = pulls
				}
			}
			results[index] = result
		}()
	}
	wg.Wait()
	snap.Runners = results

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

func collectRepo(result *runnerResult, now time.Time, windowDays, windowCount int) error {
	meta, err := ghJSON("repos/" + result.Repo)
	if err != nil {
		return fmt.Errorf("repo metadata: %w", err)
	}
	result.Stars = intField(meta, "stargazers_count")
	result.Forks = intField(meta, "forks_count")
	result.Archived = boolField(meta, "archived")
	result.PushedAt = stringField(meta, "pushed_at")

	latest, releaseCount, downloads, err := collectReleases(result.Repo)
	if err != nil {
		result.Errors = append(result.Errors, "releases: "+err.Error())
	} else {
		result.LatestRelease = latest
		result.ReleaseCount = releaseCount
		result.AssetDownloads = downloads
	}

	counts, err := forkWindows(result.Repo, result.Forks, now, windowDays, windowCount)
	if err != nil {
		return fmt.Errorf("fork windows: %w", err)
	}
	result.ForkWindows = counts

	current, err := commitCount(result.Repo, now.AddDate(0, 0, -90), now)
	if err != nil {
		result.Errors = append(result.Errors, "current commit window: "+err.Error())
	} else {
		previous, err := commitCount(result.Repo, now.AddDate(0, 0, -180), now.AddDate(0, 0, -90))
		if err != nil {
			result.Errors = append(result.Errors, "previous commit window: "+err.Error())
		} else {
			result.CommitWindows = []int{current, previous}
		}
	}
	return nil
}

type forkPageLoader func(page int) ([]map[string]any, error)

func forkWindows(repo string, totalForks int, now time.Time, windowDays, windowCount int) ([]int, error) {
	return forkWindowsWithLoader(totalForks, now, windowDays, windowCount, func(page int) ([]map[string]any, error) {
		path := fmt.Sprintf("repos/%s/forks?sort=newest&per_page=100&page=%d", repo, page)
		return ghArray(path)
	})
}

// forkWindowsWithLoader finds each time boundary with binary search over the
// newest-first fork pages. Complete pages between boundaries do not need to be
// downloaded; only boundary pages are inspected and all fetched pages are
// cached across windows.
func forkWindowsWithLoader(totalForks int, now time.Time, windowDays, windowCount int, load forkPageLoader) ([]int, error) {
	if windowDays <= 0 || windowCount <= 0 || totalForks < 0 {
		return nil, fmt.Errorf("invalid fork-window configuration")
	}
	counts := make([]int, windowCount)
	if totalForks == 0 {
		return counts, nil
	}

	pages := (totalForks + 99) / 100
	cache := make(map[int][]map[string]any)
	page := func(number int) ([]map[string]any, error) {
		if items, ok := cache[number]; ok {
			return items, nil
		}
		items, err := load(number)
		if err != nil {
			return nil, err
		}
		cache[number] = items
		return items, nil
	}

	countAfter := func(cutoff time.Time) (int, error) {
		lo, hi := 1, pages
		boundary := pages + 1
		for lo <= hi {
			mid := lo + (hi-lo)/2
			items, err := page(mid)
			if err != nil {
				return 0, err
			}
			if len(items) == 0 {
				boundary = mid
				hi = mid - 1
				continue
			}
			last, err := time.Parse(time.RFC3339, stringField(items[len(items)-1], "created_at"))
			if err != nil {
				return 0, fmt.Errorf("page %d last fork timestamp: %w", mid, err)
			}
			if last.After(cutoff) {
				lo = mid + 1
			} else {
				boundary = mid
				hi = mid - 1
			}
		}

		if boundary == pages+1 {
			lastPage, err := page(pages)
			if err != nil {
				return 0, err
			}
			return (pages-1)*100 + len(lastPage), nil
		}

		items, err := page(boundary)
		if err != nil {
			return 0, err
		}
		prefix := 0
		for _, item := range items {
			created, err := time.Parse(time.RFC3339, stringField(item, "created_at"))
			if err != nil {
				return 0, fmt.Errorf("page %d fork timestamp: %w", boundary, err)
			}
			if !created.After(cutoff) {
				break
			}
			prefix++
		}
		return (boundary-1)*100 + prefix, nil
	}

	previous, err := countAfter(now)
	if err != nil {
		return nil, err
	}
	for index := 0; index < windowCount; index++ {
		cutoff := now.AddDate(0, 0, -windowDays*(index+1))
		cumulative, err := countAfter(cutoff)
		if err != nil {
			return nil, err
		}
		counts[index] = cumulative - previous
		previous = cumulative
	}
	return counts, nil
}

func collectReleases(repo string) (string, int, int, error) {
	latest := ""
	count := 0
	downloads := 0
	for page := 1; ; page++ {
		releases, err := ghArray(fmt.Sprintf("repos/%s/releases?per_page=100&page=%d", repo, page))
		if err != nil {
			return "", 0, 0, err
		}
		if page == 1 && len(releases) > 0 {
			latest = stringField(releases[0], "published_at")
		}
		for _, release := range releases {
			count++
			assets, _ := release["assets"].([]any)
			for _, raw := range assets {
				asset, _ := raw.(map[string]any)
				downloads += intField(asset, "download_count")
			}
		}
		if len(releases) < 100 {
			return latest, count, downloads, nil
		}
	}
}

func commitCount(repo string, since, until time.Time) (int, error) {
	query := url.Values{}
	query.Set("since", since.UTC().Format(time.RFC3339))
	query.Set("until", until.UTC().Format(time.RFC3339))
	query.Set("per_page", "1")
	body, headers, err := ghResponse("repos/" + repo + "/commits?" + query.Encode())
	if err != nil {
		return 0, err
	}
	var commits []map[string]any
	if err := json.Unmarshal(body, &commits); err != nil {
		return 0, err
	}
	if len(commits) == 0 {
		return 0, nil
	}
	if last := lastPage(headers.Get("Link")); last > 0 {
		return last, nil
	}
	return len(commits), nil
}

func lastPage(link string) int {
	for _, part := range strings.Split(link, ",") {
		if !strings.Contains(part, `rel="last"`) {
			continue
		}
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start < 0 || end <= start {
			return 0
		}
		u, err := url.Parse(part[start+1 : end])
		if err != nil {
			return 0
		}
		var page int
		_, _ = fmt.Sscanf(u.Query().Get("page"), "%d", &page)
		return page
	}
	return 0
}

func collectDockerHubPulls(repo string) (int, error) {
	body, err := httpGet("https://hub.docker.com/v2/repositories/" + repo + "/")
	if err != nil {
		return 0, fmt.Errorf("docker hub %s: %w", repo, err)
	}
	var payload struct {
		PullCount int `json:"pull_count"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, fmt.Errorf("docker hub %s: %w", repo, err)
	}
	return payload.PullCount, nil
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
// once rather than spawning a subprocess per request. Even with boundary search,
// process startup would otherwise dominate the collector's runtime.
func gh(path string) ([]byte, error) {
	body, _, err := ghResponse(path)
	return body, err
}

func ghResponse(path string) ([]byte, http.Header, error) {
	token, err := githubToken()
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/"+strings.TrimPrefix(path, "/"), nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := githubClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("GET %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.Header, fmt.Errorf("GET %s: status %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	return body, resp.Header, nil
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
