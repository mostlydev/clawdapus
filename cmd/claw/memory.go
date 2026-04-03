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
	memoryForgetURL     string
	memoryForgetToken   string
	memoryForgetAgent   []string
	memoryForgetEntryID []string
	memoryForgetReason  string

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
	Forget  *memoryOpEntry   `json:"forget,omitempty"`
	Auth    *memoryAuthEntry `json:"auth,omitempty"`
}

type memoryTarget struct {
	PodDir      string
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

const memoryForgetTombstoneVersion = 1

type memoryForgetTombstone struct {
	Version       int    `json:"version"`
	TS            string `json:"ts"`
	AgentID       string `json:"agent_id"`
	MemoryService string `json:"memory_service"`
	EntryID       string `json:"entry_id"`
	Reason        string `json:"reason,omitempty"`
}

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
			fmt.Fprintf(cmd.OutOrStdout(), "Skipped %d agent%s with no eligible history entries\n", summary.SkippedAgents, countPlural(summary.SkippedAgents))
		}
		return nil
	},
}

var memoryForgetCmd = &cobra.Command{
	Use:   "forget <memory-service>",
	Short: "Forget retained memory by stable session-history entry ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(memoryForgetAgent) == 0 {
			return fmt.Errorf("at least one --agent is required")
		}

		entryIDs, err := normalizeForgetEntryIDs(memoryForgetEntryID)
		if err != nil {
			return err
		}

		generatedPath, err := resolveComposeGeneratedPath()
		if err != nil {
			return err
		}
		podDir := filepath.Dir(generatedPath)

		targets, err := discoverMemoryForgetTargets(podDir, args[0], memoryForgetAgent)
		if err != nil {
			return err
		}
		if err := validateSharedMemoryTargetShape(targets, strings.TrimSpace(memoryForgetURL) != "", "forget"); err != nil {
			return err
		}
		forgetURL, err := resolveMemoryForgetURL(generatedPath, targets[0].Manifest, memoryForgetURL)
		if err != nil {
			return err
		}

		summary, err := forgetMemoryEntries(cmd.OutOrStdout(), forgetURL, strings.TrimSpace(memoryForgetToken), targets, entryIDs, strings.TrimSpace(memoryForgetReason))
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Forgot %d entr%s across %d agent%s via %s\n",
			summary.Entries,
			entryPlural(summary.Entries),
			summary.Agents,
			countPlural(summary.Agents),
			forgetURL,
		)
		if summary.AlreadyForgotten > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "Skipped %d already-tombstoned entr%s\n", summary.AlreadyForgotten, entryPlural(summary.AlreadyForgotten))
		}
		return nil
	},
}

type memoryBackfillSummary struct {
	Entries       int
	Agents        int
	SkippedAgents int
}

type memoryForgetSummary struct {
	Entries          int
	Agents           int
	AlreadyForgotten int
}

func discoverMemoryBackfillTargets(podDir, memoryService string, requestedAgents []string) ([]memoryTarget, error) {
	return discoverMemoryTargets(podDir, memoryService, requestedAgents, "retain")
}

func discoverMemoryForgetTargets(podDir, memoryService string, requestedAgents []string) ([]memoryTarget, error) {
	return discoverMemoryTargets(podDir, memoryService, requestedAgents, "forget")
}

func discoverMemoryTargets(podDir, memoryService string, requestedAgents []string, operation string) ([]memoryTarget, error) {
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

	targets := make([]memoryTarget, 0)
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
		if manifestOperationPath(manifest, operation) == "" {
			return nil, fmt.Errorf("memory service %q has no %s endpoint for agent %q", memoryService, operation, agentID)
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

		targets = append(targets, memoryTarget{
			PodDir:      podDir,
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

func validateSharedBackfillTargetShape(targets []memoryTarget, urlOverride bool) error {
	return validateSharedMemoryTargetShape(targets, urlOverride, "retain")
}

func validateSharedMemoryTargetShape(targets []memoryTarget, urlOverride bool, operation string) error {
	if len(targets) == 0 {
		return nil
	}
	first := targets[0].Manifest
	firstPath := manifestOperationPath(first, operation)
	for _, target := range targets[1:] {
		currentPath := manifestOperationPath(target.Manifest, operation)
		if !urlOverride && target.Manifest.BaseURL != first.BaseURL {
			return fmt.Errorf("memory service %q has inconsistent base URLs across subscribed agents; pass --url to override", first.Service)
		}
		if currentPath != firstPath {
			return fmt.Errorf("memory service %q has inconsistent %s paths across subscribed agents", first.Service, operation)
		}
	}
	return nil
}

func resolveMemoryBackfillURL(composePath string, manifest memoryManifestFile, override string) (string, error) {
	return resolveMemoryOperationURL(composePath, manifest, override, "retain")
}

func resolveMemoryForgetURL(composePath string, manifest memoryManifestFile, override string) (string, error) {
	return resolveMemoryOperationURL(composePath, manifest, override, "forget")
}

func resolveMemoryOperationURL(composePath string, manifest memoryManifestFile, override, operation string) (string, error) {
	opPath := manifestOperationPath(manifest, operation)
	if opPath == "" {
		return "", fmt.Errorf("memory manifest has no %s endpoint", operation)
	}
	if strings.TrimSpace(override) != "" {
		return joinMemoryOperationURL(override, opPath)
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
		Path:   opPath,
	}).String(), nil
}

func manifestOperationPath(manifest memoryManifestFile, operation string) string {
	switch operation {
	case "retain":
		if manifest.Retain == nil {
			return ""
		}
		return strings.TrimSpace(manifest.Retain.Path)
	case "forget":
		if manifest.Forget == nil {
			return ""
		}
		return strings.TrimSpace(manifest.Forget.Path)
	default:
		return ""
	}
}

func joinMemoryOperationURL(raw, opPath string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse --url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("--url must include scheme and host")
	}
	if strings.TrimSpace(u.Path) == "" || u.Path == "/" {
		u.Path = opPath
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

type memoryForgetRequest struct {
	AgentID  string         `json:"agent_id"`
	Pod      string         `json:"pod,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
	EntryIDs []string       `json:"entry_ids"`
	Reason   string         `json:"reason,omitempty"`
}

func replayMemoryBackfill(stdout io.Writer, retainURL, authToken string, targets []memoryTarget, after *time.Time, limit int) (memoryBackfillSummary, error) {
	var summary memoryBackfillSummary
	client := &http.Client{}

	for _, target := range targets {
		tombstones, err := loadMemoryTombstoneIDSet(target.PodDir, target.AgentID, target.Manifest.Service)
		if err != nil {
			return summary, fmt.Errorf("load tombstones for %q: %w", target.AgentID, err)
		}
		replayed, err := replayHistoryFileToMemory(client, retainURL, effectiveMemoryAuthToken(authToken, target.Manifest), backfillRetainTimeout(target.Manifest), target, tombstones, after, limit)
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

func effectiveMemoryAuthToken(override string, manifest memoryManifestFile) string {
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

func forgetTimeout(manifest memoryManifestFile) time.Duration {
	if manifest.Forget != nil && manifest.Forget.TimeoutMS > 0 {
		return time.Duration(manifest.Forget.TimeoutMS) * time.Millisecond
	}
	return defaultMemoryBackfillTimeout
}

func replayHistoryFileToMemory(client *http.Client, retainURL, authToken string, timeout time.Duration, target memoryTarget, tombstones map[string]struct{}, after *time.Time, limit int) (int, error) {
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
		if _, forgotten := tombstones[meta.ID]; forgotten {
			continue
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

func postRetainBackfill(client *http.Client, retainURL, authToken string, timeout time.Duration, target memoryTarget, requestedModel string, rawEntry []byte) error {
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

func normalizeForgetEntryIDs(raw []string) ([]string, error) {
	seen := make(map[string]struct{}, len(raw))
	entryIDs := make([]string, 0, len(raw))
	for _, entryID := range raw {
		entryID = strings.TrimSpace(entryID)
		if entryID == "" {
			continue
		}
		if _, exists := seen[entryID]; exists {
			continue
		}
		seen[entryID] = struct{}{}
		entryIDs = append(entryIDs, entryID)
	}
	if len(entryIDs) == 0 {
		return nil, fmt.Errorf("at least one --entry-id is required")
	}
	return entryIDs, nil
}

func forgetMemoryEntries(stdout io.Writer, forgetURL, authToken string, targets []memoryTarget, entryIDs []string, reason string) (memoryForgetSummary, error) {
	var summary memoryForgetSummary
	client := &http.Client{}

	for _, target := range targets {
		existing, err := loadMemoryForgetTombstones(target.PodDir, target.AgentID, target.Manifest.Service)
		if err != nil {
			return summary, fmt.Errorf("load tombstones for %q: %w", target.AgentID, err)
		}

		pending := filterUntombstonedEntryIDs(entryIDs, existing)
		summary.AlreadyForgotten += len(entryIDs) - len(pending)
		if len(pending) == 0 {
			continue
		}

		if err := ensureForgetEntryIDsExist(target.HistoryPath, pending); err != nil {
			return summary, fmt.Errorf("forget %q: %w", target.AgentID, err)
		}
		if err := postForgetRequest(client, forgetURL, effectiveMemoryAuthToken(authToken, target.Manifest), forgetTimeout(target.Manifest), target, pending, reason); err != nil {
			return summary, fmt.Errorf("forget %q: %w", target.AgentID, err)
		}
		if err := appendMemoryForgetTombstones(target.PodDir, target, pending, reason, time.Now().UTC()); err != nil {
			return summary, fmt.Errorf("forget %q: write tombstones: %w", target.AgentID, err)
		}

		summary.Agents++
		summary.Entries += len(pending)
		fmt.Fprintf(stdout, "Forgot %d entr%s for %s\n", len(pending), entryPlural(len(pending)), target.AgentID)
	}

	return summary, nil
}

func ensureForgetEntryIDsExist(historyPath string, entryIDs []string) error {
	f, err := os.Open(historyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no session history found at %q", historyPath)
		}
		return err
	}
	defer f.Close()

	needed := make(map[string]struct{}, len(entryIDs))
	for _, entryID := range entryIDs {
		needed[entryID] = struct{}{}
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		_, meta, err := ensureHistoryEntryID(line)
		if err != nil {
			return err
		}
		delete(needed, meta.ID)
		if len(needed) == 0 {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(needed) == 0 {
		return nil
	}

	missing := make([]string, 0, len(needed))
	for entryID := range needed {
		missing = append(missing, entryID)
	}
	sort.Strings(missing)
	return fmt.Errorf("history does not contain entry id%s %s", countPlural(len(missing)), strings.Join(missing, ", "))
}

func postForgetRequest(client *http.Client, forgetURL, authToken string, timeout time.Duration, target memoryTarget, entryIDs []string, reason string) error {
	payload, err := json.Marshal(memoryForgetRequest{
		AgentID:  target.AgentID,
		Pod:      target.Pod,
		Metadata: forgetMetadata(target.Metadata),
		EntryIDs: append([]string(nil), entryIDs...),
		Reason:   reason,
	})
	if err != nil {
		return fmt.Errorf("marshal forget payload: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, forgetURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build forget request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if strings.TrimSpace(authToken) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(authToken))
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send forget request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("forget returned status %d: %s", resp.StatusCode, msg)
	}
	return nil
}

func forgetMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return nil
	}
	out := map[string]any{
		"service": stringFromMap(metadata, "service"),
		"type":    stringFromMap(metadata, "type"),
		"path":    "forget",
	}
	if timezone := stringFromMap(metadata, "timezone"); timezone != "" {
		out["timezone"] = timezone
	}
	return out
}

func loadMemoryTombstoneIDSet(podDir, agentID, memoryService string) (map[string]struct{}, error) {
	entries, err := loadMemoryForgetTombstones(podDir, agentID, memoryService)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]struct{}, len(entries))
	for entryID := range entries {
		ids[entryID] = struct{}{}
	}
	return ids, nil
}

func loadMemoryForgetTombstones(podDir, agentID, memoryService string) (map[string]memoryForgetTombstone, error) {
	entries := make(map[string]memoryForgetTombstone)
	if strings.TrimSpace(podDir) == "" {
		return entries, nil
	}

	path := memoryForgetTombstonePath(podDir, agentID)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return entries, nil
		}
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var tombstone memoryForgetTombstone
		if err := json.Unmarshal(line, &tombstone); err != nil {
			return nil, fmt.Errorf("parse tombstone entry: %w", err)
		}
		if strings.TrimSpace(tombstone.MemoryService) != memoryService {
			continue
		}
		entryID := strings.TrimSpace(tombstone.EntryID)
		if entryID == "" {
			continue
		}
		entries[entryID] = tombstone
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func appendMemoryForgetTombstones(podDir string, target memoryTarget, entryIDs []string, reason string, now time.Time) error {
	if strings.TrimSpace(podDir) == "" {
		return fmt.Errorf("memory tombstone pod dir is required")
	}

	path := memoryForgetTombstonePath(podDir, target.AgentID)
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o666)
	if err != nil {
		return err
	}
	defer f.Close()

	for _, entryID := range entryIDs {
		line, err := json.Marshal(memoryForgetTombstone{
			Version:       memoryForgetTombstoneVersion,
			TS:            now.Format(time.RFC3339),
			AgentID:       target.AgentID,
			MemoryService: target.Manifest.Service,
			EntryID:       entryID,
			Reason:        reason,
		})
		if err != nil {
			return fmt.Errorf("marshal tombstone entry: %w", err)
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func filterUntombstonedEntryIDs(entryIDs []string, existing map[string]memoryForgetTombstone) []string {
	if len(existing) == 0 {
		return append([]string(nil), entryIDs...)
	}
	pending := make([]string, 0, len(entryIDs))
	for _, entryID := range entryIDs {
		if _, ok := existing[entryID]; ok {
			continue
		}
		pending = append(pending, entryID)
	}
	return pending
}

func memoryForgetTombstonePath(podDir, agentID string) string {
	return filepath.Join(podDir, ".claw-memory-tombstones", agentID, "tombstones.jsonl")
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
	memoryForgetCmd.Flags().StringVar(&memoryForgetURL, "url", "", "Override the memory forget endpoint URL (defaults to the published service URL)")
	memoryForgetCmd.Flags().StringVar(&memoryForgetToken, "auth-token", "", "Override the bearer token used for the memory forget endpoint")
	memoryForgetCmd.Flags().StringSliceVar(&memoryForgetAgent, "agent", nil, "Restrict forget to specific agent IDs")
	memoryForgetCmd.Flags().StringSliceVar(&memoryForgetEntryID, "entry-id", nil, "Stable session-history entry ID to forget (repeatable)")
	memoryForgetCmd.Flags().StringVar(&memoryForgetReason, "reason", "", "Optional governed reason recorded with the tombstone")
	memoryBackfillCmd.Flags().StringVar(&memoryBackfillAfter, "after", "", "Only replay entries after this RFC3339 timestamp")
	memoryBackfillCmd.Flags().IntVar(&memoryBackfillLimit, "limit", 0, "Maximum entries to replay per agent (0 means all)")
	memoryBackfillCmd.Flags().StringVar(&memoryBackfillURL, "url", "", "Override the memory retain endpoint URL (defaults to the published service URL)")
	memoryBackfillCmd.Flags().StringVar(&memoryBackfillToken, "auth-token", "", "Override the bearer token used for the memory retain endpoint")
	memoryBackfillCmd.Flags().StringSliceVar(&memoryBackfillAgent, "agent", nil, "Restrict backfill to specific agent IDs")
	memoryCmd.AddCommand(memoryForgetCmd)
	memoryCmd.AddCommand(memoryBackfillCmd)
	rootCmd.AddCommand(memoryCmd)
}
