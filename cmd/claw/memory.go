package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	memoryBackfillAfter string
	memoryBackfillLimit int
	memoryBackfillURL   string
	memoryBackfillToken string
	memoryBackfillAgent []string

	runComposeOutputCommand = runComposeOutputCommandDefault
)

type memoryAuthEntry struct {
	Type  string `json:"type"`
	Token string `json:"token,omitempty"`
}

type memoryOpEntry struct {
	Path      string `json:"path"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type memoryManifestFile struct {
	Service string           `json:"service"`
	BaseURL string           `json:"base_url"`
	Retain  *memoryOpEntry   `json:"retain,omitempty"`
	Auth    *memoryAuthEntry `json:"auth,omitempty"`
}

type memoryBackfillTarget struct {
	AgentID     string
	HistoryPath string
	Pod         string
	Metadata    map[string]any
	Manifest    memoryManifestFile
}

type backfillComposeFile struct {
	Services map[string]backfillComposeService `yaml:"services"`
}

type backfillComposeService struct {
	Ports []any `yaml:"ports"`
}

const defaultMemoryBackfillTimeout = 10 * time.Second

var memoryCmd = &cobra.Command{
	Use:   "memory",
	Short: "Operate on configured memory services",
}

var memoryBackfillCmd = &cobra.Command{
	Use:   "backfill <memory-service>",
	Short: "Replay retained session history to a memory service retain endpoint",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		generatedPath, err := resolveComposeGeneratedPath()
		if err != nil {
			return err
		}

		after, err := parseHistoryAfter(memoryBackfillAfter)
		if err != nil {
			return err
		}
		limit, err := normalizeBackfillLimit(memoryBackfillLimit)
		if err != nil {
			return err
		}

		podDir := filepath.Dir(generatedPath)
		targets, err := discoverMemoryBackfillTargets(podDir, args[0], memoryBackfillAgent)
		if err != nil {
			return err
		}
		if err := validateSharedBackfillTargetShape(targets, strings.TrimSpace(memoryBackfillURL) != ""); err != nil {
			return err
		}
		retainURL, err := resolveMemoryBackfillURL(generatedPath, targets[0].Manifest, memoryBackfillURL)
		if err != nil {
			return err
		}

		summary, err := replayMemoryBackfill(cmd.OutOrStdout(), retainURL, strings.TrimSpace(memoryBackfillToken), targets, after, limit)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Backfilled %d entr%s across %d agent%s to %s\n",
			summary.Entries,
			entryPlural(summary.Entries),
			summary.Agents,
			countPlural(summary.Agents),
			retainURL,
		)
		if summary.SkippedAgents > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "Skipped %d agent%s with no matching history entries\n", summary.SkippedAgents, countPlural(summary.SkippedAgents))
		}
		return nil
	},
}

type memoryBackfillSummary struct {
	Entries       int
	Agents        int
	SkippedAgents int
}

func discoverMemoryBackfillTargets(podDir, memoryService string, requestedAgents []string) ([]memoryBackfillTarget, error) {
	contextRoot := filepath.Join(podDir, ".claw-runtime", "context")
	entries, err := os.ReadDir(contextRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no generated agent context found in %q (run 'claw up' first)", contextRoot)
		}
		return nil, fmt.Errorf("list generated agent context: %w", err)
	}

	requested := make(map[string]struct{}, len(requestedAgents))
	for _, agentID := range requestedAgents {
		agentID = strings.TrimSpace(agentID)
		if agentID == "" {
			continue
		}
		requested[agentID] = struct{}{}
	}
	foundRequested := make(map[string]struct{}, len(requested))

	targets := make([]memoryBackfillTarget, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		agentID := entry.Name()
		if len(requested) > 0 {
			if _, ok := requested[agentID]; !ok {
				continue
			}
		}

		manifestPath := filepath.Join(contextRoot, agentID, "memory.json")
		raw, err := os.ReadFile(manifestPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read memory manifest for %q: %w", agentID, err)
		}

		var manifest memoryManifestFile
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return nil, fmt.Errorf("parse memory manifest for %q: %w", agentID, err)
		}
		if manifest.Service != memoryService {
			continue
		}
		if manifest.Retain == nil || strings.TrimSpace(manifest.Retain.Path) == "" {
			return nil, fmt.Errorf("memory service %q has no retain endpoint for agent %q", memoryService, agentID)
		}

		metaPath := filepath.Join(contextRoot, agentID, "metadata.json")
		metaRaw, err := os.ReadFile(metaPath)
		if err != nil {
			return nil, fmt.Errorf("read metadata for %q: %w", agentID, err)
		}
		var metadata map[string]any
		if err := json.Unmarshal(metaRaw, &metadata); err != nil {
			return nil, fmt.Errorf("parse metadata for %q: %w", agentID, err)
		}

		targets = append(targets, memoryBackfillTarget{
			AgentID:     agentID,
			HistoryPath: filepath.Join(podDir, ".claw-session-history", agentID, "history.jsonl"),
			Pod:         stringFromMap(metadata, "pod"),
			Metadata:    metadata,
			Manifest:    manifest,
		})
		foundRequested[agentID] = struct{}{}
	}

	sort.Slice(targets, func(i, j int) bool {
		return targets[i].AgentID < targets[j].AgentID
	})

	if len(requested) > 0 {
		missing := make([]string, 0)
		for agentID := range requested {
			if _, ok := foundRequested[agentID]; !ok {
				missing = append(missing, agentID)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			return nil, fmt.Errorf("memory service %q is not subscribed by agent%s %s", memoryService, countPlural(len(missing)), strings.Join(missing, ", "))
		}
	}

	if len(targets) == 0 {
		return nil, fmt.Errorf("no agents subscribe to memory service %q", memoryService)
	}
	return targets, nil
}

// Unlike `claw history export`, a backfill limit of 0 means "replay all"
// because this command is primarily used for full rebuilds and recovery.
func normalizeBackfillLimit(limit int) (int, error) {
	if limit < 0 {
		return 0, fmt.Errorf("limit must be >= 0")
	}
	return limit, nil
}

func validateSharedBackfillTargetShape(targets []memoryBackfillTarget, urlOverride bool) error {
	if len(targets) == 0 {
		return nil
	}
	first := targets[0].Manifest
	firstPath := manifestRetainPath(first)
	for _, target := range targets[1:] {
		currentPath := manifestRetainPath(target.Manifest)
		if !urlOverride && target.Manifest.BaseURL != first.BaseURL {
			return fmt.Errorf("memory service %q has inconsistent base URLs across subscribed agents; pass --url to override", first.Service)
		}
		if currentPath != firstPath {
			return fmt.Errorf("memory service %q has inconsistent retain paths across subscribed agents", first.Service)
		}
	}
	return nil
}

func resolveMemoryBackfillURL(composePath string, manifest memoryManifestFile, override string) (string, error) {
	retainPath := manifestRetainPath(manifest)
	if retainPath == "" {
		return "", fmt.Errorf("memory manifest has no retain endpoint")
	}
	if strings.TrimSpace(override) != "" {
		return joinRetainURL(override, retainPath)
	}

	baseURL, err := url.Parse(strings.TrimSpace(manifest.BaseURL))
	if err != nil {
		return "", fmt.Errorf("parse memory base URL %q: %w", manifest.BaseURL, err)
	}
	service := strings.TrimSpace(baseURL.Hostname())
	port := strings.TrimSpace(baseURL.Port())
	if service == "" || port == "" {
		return "", fmt.Errorf("memory base URL %q must include service host and port", manifest.BaseURL)
	}

	hostPort, err := resolvePublishedHostPort(composePath, service, port)
	if err != nil {
		return "", fmt.Errorf("resolve host URL for memory service %q: %w (pass --url to override)", service, err)
	}
	return (&url.URL{
		Scheme: "http",
		Host:   hostPort,
		Path:   retainPath,
	}).String(), nil
}

func manifestRetainPath(manifest memoryManifestFile) string {
	if manifest.Retain == nil {
		return ""
	}
	return strings.TrimSpace(manifest.Retain.Path)
}

func joinRetainURL(raw, retainPath string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse --url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("--url must include scheme and host")
	}
	if strings.TrimSpace(u.Path) == "" || u.Path == "/" {
		u.Path = retainPath
	}
	return u.String(), nil
}

func resolvePublishedHostPort(composePath, service, containerPort string) (string, error) {
	if hostPort, err := resolveHostPortFromComposeFile(composePath, service, containerPort); err == nil {
		return hostPort, nil
	}
	out, err := runComposeOutputCommand("compose", "-f", composePath, "port", service, containerPort)
	if err != nil {
		return "", formatComposeOutputError("docker compose port", err, out)
	}
	hostPort := strings.TrimSpace(string(out))
	if hostPort == "" {
		return "", fmt.Errorf("docker compose port returned no host binding")
	}
	return normalizeHostPort(hostPort), nil
}

func resolveHostPortFromComposeFile(composePath, service, containerPort string) (string, error) {
	raw, err := os.ReadFile(composePath)
	if err != nil {
		return "", err
	}
	var compose backfillComposeFile
	if err := yaml.Unmarshal(raw, &compose); err != nil {
		return "", fmt.Errorf("parse compose file: %w", err)
	}
	svc, ok := compose.Services[service]
	if !ok {
		return "", fmt.Errorf("service %q not found in compose.generated.yml", service)
	}
	for _, portEntry := range svc.Ports {
		hostPort, ok := matchComposePortEntry(portEntry, containerPort)
		if ok {
			return normalizeHostPort(hostPort), nil
		}
	}
	return "", fmt.Errorf("service %q does not publish container port %s with a deterministic host port", service, containerPort)
}

func matchComposePortEntry(entry any, containerPort string) (string, bool) {
	switch v := entry.(type) {
	case string:
		return parseComposePortString(v, containerPort)
	case map[string]any:
		return parseComposePortMap(v, containerPort)
	case map[any]any:
		converted := make(map[string]any, len(v))
		for key, value := range v {
			converted[fmt.Sprint(key)] = value
		}
		return parseComposePortMap(converted, containerPort)
	default:
		return "", false
	}
}

func parseComposePortString(raw, containerPort string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if idx := strings.Index(raw, "/"); idx >= 0 {
		raw = raw[:idx]
	}
	parts := strings.Split(raw, ":")
	if len(parts) < 2 {
		return "", false
	}

	target := strings.TrimSpace(parts[len(parts)-1])
	if target != containerPort {
		return "", false
	}
	published := strings.TrimSpace(parts[len(parts)-2])
	if published == "" {
		return "", false
	}
	host := "127.0.0.1"
	if len(parts) > 2 {
		host = strings.Trim(strings.Join(parts[:len(parts)-2], ":"), "[]")
	}
	return net.JoinHostPort(normalizeHost(host), published), true
}

func parseComposePortMap(raw map[string]any, containerPort string) (string, bool) {
	target := strings.TrimSpace(fmt.Sprint(raw["target"]))
	if target != containerPort {
		return "", false
	}
	published := strings.TrimSpace(fmt.Sprint(raw["published"]))
	if published == "" || published == "<nil>" {
		return "", false
	}
	host := normalizeHost(strings.Trim(strings.TrimSpace(fmt.Sprint(raw["host_ip"])), "[]"))
	return net.JoinHostPort(host, published), true
}

func normalizeHostPort(hostPort string) string {
	hostPort = strings.TrimSpace(hostPort)
	if hostPort == "" {
		return hostPort
	}
	if strings.HasPrefix(hostPort, ":::") {
		return net.JoinHostPort("127.0.0.1", strings.TrimPrefix(hostPort, ":::"))
	}
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		return hostPort
	}
	return net.JoinHostPort(normalizeHost(strings.Trim(host, "[]")), port)
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	switch host {
	case "", "0.0.0.0", "::":
		return "127.0.0.1"
	default:
		return host
	}
}

func formatComposeOutputError(prefix string, err error, out []byte) error {
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		return fmt.Errorf("%s failed: %w", prefix, err)
	}
	return fmt.Errorf("%s failed: %s", prefix, msg)
}

type backfillRetainRequest struct {
	AgentID  string          `json:"agent_id"`
	Pod      string          `json:"pod,omitempty"`
	Metadata map[string]any  `json:"metadata,omitempty"`
	Entry    json.RawMessage `json:"entry"`
}

func replayMemoryBackfill(stdout io.Writer, retainURL, authToken string, targets []memoryBackfillTarget, after *time.Time, limit int) (memoryBackfillSummary, error) {
	var summary memoryBackfillSummary
	client := &http.Client{}

	for _, target := range targets {
		replayed, err := replayHistoryFileToMemory(client, retainURL, effectiveBackfillAuthToken(authToken, target.Manifest), backfillRetainTimeout(target.Manifest), target, after, limit)
		if err != nil {
			return summary, fmt.Errorf("backfill %q: %w", target.AgentID, err)
		}
		if replayed == 0 {
			summary.SkippedAgents++
			continue
		}
		summary.Agents++
		summary.Entries += replayed
		fmt.Fprintf(stdout, "Replayed %d entr%s for %s\n", replayed, entryPlural(replayed), target.AgentID)
	}

	return summary, nil
}

func effectiveBackfillAuthToken(override string, manifest memoryManifestFile) string {
	if strings.TrimSpace(override) != "" {
		return strings.TrimSpace(override)
	}
	if manifest.Auth != nil && strings.EqualFold(manifest.Auth.Type, "bearer") {
		return manifest.Auth.Token
	}
	return ""
}

func backfillRetainTimeout(manifest memoryManifestFile) time.Duration {
	if manifest.Retain != nil && manifest.Retain.TimeoutMS > 0 {
		return time.Duration(manifest.Retain.TimeoutMS) * time.Millisecond
	}
	return defaultMemoryBackfillTimeout
}

func replayHistoryFileToMemory(client *http.Client, retainURL, authToken string, timeout time.Duration, target memoryBackfillTarget, after *time.Time, limit int) (int, error) {
	f, err := os.Open(target.HistoryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	replayed := 0
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		lineWithID, meta, err := ensureHistoryEntryID(line)
		if err != nil {
			return replayed, err
		}
		if limit > 0 && replayed >= limit {
			break
		}
		if after != nil {
			ts, err := time.Parse(time.RFC3339, meta.TS)
			if err != nil {
				return replayed, fmt.Errorf("parse history timestamp %q: %w", meta.TS, err)
			}
			if !ts.After(*after) {
				continue
			}
		}
		if err := postRetainBackfill(client, retainURL, authToken, timeout, target, meta.RequestedModel, lineWithID); err != nil {
			return replayed, err
		}
		replayed++
	}
	if err := scanner.Err(); err != nil {
		return replayed, err
	}
	return replayed, nil
}

func postRetainBackfill(client *http.Client, retainURL, authToken string, timeout time.Duration, target memoryBackfillTarget, requestedModel string, rawEntry []byte) error {
	payload, err := json.Marshal(backfillRetainRequest{
		AgentID:  target.AgentID,
		Pod:      target.Pod,
		Metadata: backfillMetadata(target.Metadata, requestedModel),
		Entry:    json.RawMessage(append([]byte(nil), rawEntry...)),
	})
	if err != nil {
		return fmt.Errorf("marshal retain payload: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, retainURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build retain request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if strings.TrimSpace(authToken) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(authToken))
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send retain request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("retain returned status %d: %s", resp.StatusCode, msg)
	}
	return nil
}

// backfillMetadata intentionally mirrors only the stable subset of live retain
// metadata. Replay is derived from the ledger, so it does not attempt to
// reconstruct transient per-request state beyond fields that materially affect
// memory indexing or policy.
func backfillMetadata(metadata map[string]any, requestedModel string) map[string]any {
	if metadata == nil {
		return nil
	}
	out := map[string]any{
		"service": stringFromMap(metadata, "service"),
		"type":    stringFromMap(metadata, "type"),
		"path":    "retain",
	}
	if timezone := stringFromMap(metadata, "timezone"); timezone != "" {
		out["timezone"] = timezone
	}
	if requestedModel != "" {
		out["requested_model"] = requestedModel
	}
	return out
}

func stringFromMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	v, _ := values[key].(string)
	return v
}

func entryPlural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

func countPlural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func runComposeOutputCommandDefault(args ...string) ([]byte, error) {
	cmd := exec.Command("docker", args...)
	return cmd.CombinedOutput()
}

func init() {
	memoryBackfillCmd.Flags().StringVar(&memoryBackfillAfter, "after", "", "Only replay entries after this RFC3339 timestamp")
	memoryBackfillCmd.Flags().IntVar(&memoryBackfillLimit, "limit", 0, "Maximum entries to replay per agent (0 means all)")
	memoryBackfillCmd.Flags().StringVar(&memoryBackfillURL, "url", "", "Override the memory retain endpoint URL (defaults to the published service URL)")
	memoryBackfillCmd.Flags().StringVar(&memoryBackfillToken, "auth-token", "", "Override the bearer token used for the memory retain endpoint")
	memoryBackfillCmd.Flags().StringSliceVar(&memoryBackfillAgent, "agent", nil, "Restrict backfill to specific agent IDs")
	memoryCmd.AddCommand(memoryBackfillCmd)
	rootCmd.AddCommand(memoryCmd)
}
