package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	containerapi "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/mostlydev/clawdapus/internal/audit"
	manifestpkg "github.com/mostlydev/clawdapus/internal/clawdash"
	"github.com/mostlydev/clawdapus/internal/clawapi"
	"github.com/mostlydev/clawdapus/internal/driver"
	_ "github.com/mostlydev/clawdapus/internal/driver/hermes"
	_ "github.com/mostlydev/clawdapus/internal/driver/microclaw"
	_ "github.com/mostlydev/clawdapus/internal/driver/nanobot"
	_ "github.com/mostlydev/clawdapus/internal/driver/nanoclaw"
	_ "github.com/mostlydev/clawdapus/internal/driver/nullclaw"
	_ "github.com/mostlydev/clawdapus/internal/driver/openclaw"
	_ "github.com/mostlydev/clawdapus/internal/driver/picoclaw"
)

type apiHandler struct {
	manifest      *manifestpkg.PodManifest
	store         *clawapi.Store
	docker        *client.Client
	auditLog      *json.Encoder
	auditErr      io.Writer
	thresholds    clawapi.Thresholds
	governanceDir string
}

type serviceStatus struct {
	Service      string `json:"service"`
	Count        int    `json:"count"`
	Running      int    `json:"running"`
	Status       string `json:"status"`
	Detail       string `json:"detail,omitempty"`
	Uptime       string `json:"uptime,omitempty"`
	ClawType     string `json:"claw_type,omitempty"`
	ComposeNames []string `json:"compose_names,omitempty"`
}

type alert struct {
	Severity string `json:"severity"`
	Service  string `json:"service,omitempty"`
	ClawID   string `json:"claw_id,omitempty"`
	Summary  string `json:"summary"`
}

func newHandler(manifest *manifestpkg.PodManifest, store *clawapi.Store, docker *client.Client, auditWriter io.Writer, thresholds clawapi.Thresholds, governanceDir string) http.Handler {
	if auditWriter == nil {
		auditWriter = io.Discard
	}
	return &apiHandler{
		manifest:      manifest,
		store:         store,
		docker:        docker,
		auditLog:      json.NewEncoder(auditWriter),
		auditErr:      os.Stderr,
		thresholds:    thresholds,
		governanceDir: governanceDir,
	}
}

func (h *apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/health":
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	case r.Method == http.MethodGet && r.URL.Path == "/fleet/status":
		h.handleStatus(w, r)
		return
	case r.Method == http.MethodGet && r.URL.Path == "/fleet/logs":
		h.handleLogs(w, r)
		return
	case r.Method == http.MethodGet && r.URL.Path == "/fleet/metrics":
		h.handleMetrics(w, r)
		return
	case r.Method == http.MethodGet && r.URL.Path == "/fleet/alerts":
		h.handleAlerts(w, r)
		return
	case r.Method == http.MethodPost && r.URL.Path == "/fleet/restart":
		h.handleRestart(w, r)
		return
	case r.Method == http.MethodPost && r.URL.Path == "/fleet/quarantine":
		h.handleQuarantine(w, r)
		return
	case r.Method == http.MethodPost && r.URL.Path == "/fleet/budget/set":
		h.handleBudgetSet(w, r)
		return
	case r.Method == http.MethodPost && r.URL.Path == "/fleet/model/restrict":
		h.handleModelRestrict(w, r)
		return
	default:
		http.NotFound(w, r)
		return
	}
}

func (h *apiHandler) handleStatus(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authorize(w, r, clawapi.VerbFleetStatus, "")
	if !ok {
		return
	}
	statuses, err := h.collectStatus(r.Context(), principal)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"services": statuses})
}

func (h *apiHandler) handleLogs(w http.ResponseWriter, r *http.Request) {
	service := strings.TrimSpace(r.URL.Query().Get("service"))
	if service == "" {
		writeJSONError(w, http.StatusBadRequest, "missing service query parameter")
		return
	}
	principal, ok := h.authorize(w, r, clawapi.VerbFleetLogs, service)
	if !ok {
		return
	}
	lines, err := parseLinesArg(r.URL.Query().Get("lines"), 100)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	entries, err := h.collectLogs(r.Context(), principal, service, lines)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"service": service, "lines": entries})
}

func (h *apiHandler) handleMetrics(w http.ResponseWriter, r *http.Request) {
	clawID := strings.TrimSpace(r.URL.Query().Get("claw_id"))
	if clawID == "" {
		writeJSONError(w, http.StatusBadRequest, "missing claw_id query parameter")
		return
	}
	principal, ok := h.authorize(w, r, clawapi.VerbFleetQueryMetrics, clawID)
	if !ok {
		return
	}
	since, err := parseSinceQuery(r.URL.Query().Get("since"), time.Now().Add(-time.Hour))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	events, err := h.collectMetrics(r.Context(), principal, clawID, since)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"claw_id": clawID,
		"summary": audit.Summarize(events),
		"events":  events,
	})
}

func (h *apiHandler) handleAlerts(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authorize(w, r, clawapi.VerbFleetAlerts, "")
	if !ok {
		return
	}
	since, err := parseSinceQuery(r.URL.Query().Get("since"), time.Now().Add(-time.Hour))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	alerts, err := h.collectAlerts(r.Context(), principal, since)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pod":    h.manifest.PodName,
		"alerts": alerts,
	})
}

func (h *apiHandler) authorize(w http.ResponseWriter, r *http.Request, verb, target string) (*clawapi.Principal, bool) {
	principal, err := h.store.ResolveBearer(r.Header.Get("Authorization"))
	if err != nil {
		h.logDecision("", verb, target, false, err.Error())
		writeJSONError(w, http.StatusUnauthorized, "invalid bearer token")
		return nil, false
	}
	if !principal.AllowsVerb(verb) {
		h.logDecision(principal.Name, verb, target, false, "verb denied")
		writeJSONError(w, http.StatusForbidden, "principal is not allowed to perform this action")
		return nil, false
	}
	switch verb {
	case clawapi.VerbFleetStatus, clawapi.VerbFleetAlerts:
		if !principal.AllowsPod(h.manifest.PodName) && len(principal.Services) == 0 && len(principal.ClawIDs) == 0 {
			h.logDecision(principal.Name, verb, target, false, "scope denied")
			writeJSONError(w, http.StatusForbidden, "principal is out of scope")
			return nil, false
		}
	case clawapi.VerbFleetLogs:
		if !principal.AllowsService(h.manifest.PodName, target) {
			h.logDecision(principal.Name, verb, target, false, "service out of scope")
			writeJSONError(w, http.StatusForbidden, "service is out of scope")
			return nil, false
		}
	case clawapi.VerbFleetQueryMetrics:
		if !principal.AllowsClawID(h.manifest.PodName, target) {
			h.logDecision(principal.Name, verb, target, false, "claw_id out of scope")
			writeJSONError(w, http.StatusForbidden, "claw_id is out of scope")
			return nil, false
		}
	case clawapi.VerbFleetRestart, clawapi.VerbFleetQuarantine:
		if !principal.AllowsComposeService(h.manifest.PodName, target) {
			h.logDecision(principal.Name, verb, target, false, "compose service out of scope")
			writeJSONError(w, http.StatusForbidden, "compose service is out of scope")
			return nil, false
		}
	case clawapi.VerbFleetBudgetSet, clawapi.VerbFleetModelRestrict:
		if !principal.AllowsClawID(h.manifest.PodName, target) {
			h.logDecision(principal.Name, verb, target, false, "claw_id out of scope")
			writeJSONError(w, http.StatusForbidden, "claw_id is out of scope")
			return nil, false
		}
	}
	h.logDecision(principal.Name, verb, target, true, "")
	return principal, true
}

func (h *apiHandler) collectStatus(ctx context.Context, principal *clawapi.Principal) ([]serviceStatus, error) {
	containers, err := h.listPodContainers(ctx)
	if err != nil {
		return nil, err
	}
	statuses := make([]serviceStatus, 0)
	for serviceName, manifest := range h.manifest.Services {
		if !principal.AllowsService(h.manifest.PodName, serviceName) {
			continue
		}
		items := matchServiceContainers(containers, serviceName)
		status := serviceStatus{
			Service:  serviceName,
			Count:    manifest.Count,
			ClawType: manifest.ClawType,
		}
		if status.Count < 1 {
			status.Count = 1
		}
		for _, ctr := range items {
			status.ComposeNames = append(status.ComposeNames, composeServiceName(ctr))
			info, err := h.docker.ContainerInspect(ctx, ctr.ID)
			if err != nil {
				status.Status, status.Detail = mergeStatus(status.Status, status.Detail, "error", err.Error())
				continue
			}
			if info.State != nil && info.State.Running {
				status.Running++
				if status.Uptime == "" && info.State.StartedAt != "" {
					status.Uptime = startedAgo(info.State.StartedAt)
				}
			}
			ctrStatus, ctrDetail := containerHealth(info)
			if ctrType := info.Config.Labels["claw.type"]; ctrType != "" {
				if d, err := driver.Lookup(ctrType); err == nil {
					health, healthErr := d.HealthProbe(driver.ContainerRef{
						ContainerID: ctr.ID,
						ServiceName: composeServiceName(ctr),
					})
					if healthErr != nil {
						ctrStatus = "error"
						ctrDetail = healthErr.Error()
					} else if health != nil {
						if health.OK {
							ctrStatus = "healthy"
						} else {
							ctrStatus = "unhealthy"
						}
						ctrDetail = health.Detail
					}
				}
			}
			status.Status, status.Detail = mergeStatus(status.Status, status.Detail, ctrStatus, ctrDetail)
		}
		if len(items) == 0 {
			status.Status = "missing"
			status.Detail = "no containers found"
		}
		if status.Status == "" {
			if status.Running == status.Count {
				status.Status = "healthy"
			} else {
				status.Status = "degraded"
			}
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (h *apiHandler) collectLogs(ctx context.Context, principal *clawapi.Principal, service string, lines int) ([]string, error) {
	if !principal.AllowsService(h.manifest.PodName, service) {
		return nil, fmt.Errorf("service %q is out of scope", service)
	}
	containers, err := h.listPodContainers(ctx)
	if err != nil {
		return nil, err
	}
	items := matchServiceContainers(containers, service)
	if len(items) == 0 {
		return nil, fmt.Errorf("no containers found for service %q", service)
	}
	out := make([]string, 0)
	for _, ctr := range items {
		rc, err := h.docker.ContainerLogs(ctx, ctr.ID, containerapi.LogsOptions{
			ShowStdout: true,
			ShowStderr: true,
			Tail:       strconv.Itoa(lines),
		})
		if err != nil {
			return nil, err
		}
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		_, copyErr := stdcopy.StdCopy(&stdout, &stderr, rc)
		_ = rc.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		for _, reader := range []struct {
			stream string
			data   *bytes.Buffer
		}{
			{stream: "stdout", data: &stdout},
			{stream: "stderr", data: &stderr},
		} {
			scanner := bufio.NewScanner(reader.data)
			for scanner.Scan() {
				out = append(out, fmt.Sprintf("%s[%s] %s", composeServiceName(ctr), reader.stream, scanner.Text()))
			}
		}
	}
	return out, nil
}

func (h *apiHandler) collectMetrics(ctx context.Context, principal *clawapi.Principal, clawID string, since time.Time) ([]audit.Event, error) {
	if !principal.AllowsClawID(h.manifest.PodName, clawID) {
		return nil, fmt.Errorf("claw_id %q is out of scope", clawID)
	}
	events, _, err := audit.CollectPodEvents(ctx, h.docker, h.manifest.PodName, since)
	if err != nil {
		return nil, err
	}
	return audit.FilterEvents(events, audit.Filter{ClawID: clawID, Since: since}), nil
}

func (h *apiHandler) collectAlerts(ctx context.Context, principal *clawapi.Principal, since time.Time) ([]alert, error) {
	statuses, err := h.collectStatus(ctx, principal)
	if err != nil {
		return nil, err
	}
	events, _, err := audit.CollectPodEvents(ctx, h.docker, h.manifest.PodName, since)
	if err != nil {
		return nil, err
	}
	summary := audit.Summarize(audit.FilterEvents(events, audit.Filter{Since: since}))
	alerts := make([]alert, 0)
	for _, status := range statuses {
		if status.Status != "healthy" && status.Status != "running" {
			alerts = append(alerts, alert{
				Severity: "critical",
				Service:  status.Service,
				Summary:  fmt.Sprintf("%s is %s (%s)", status.Service, status.Status, status.Detail),
			})
		}
	}
	for _, agent := range summary.Agents {
		if !principal.AllowsClawID(h.manifest.PodName, agent.ClawID) {
			continue
		}
		for _, ta := range h.thresholds.Evaluate(agent) {
			alerts = append(alerts, alert{
				Severity: ta.Severity,
				ClawID:   agent.ClawID,
				Summary:  ta.Summary,
			})
		}
	}
	return alerts, nil
}

// --- Write plane handlers ---

type serviceTargetRequest struct {
	Service string `json:"service"`
}

type budgetSetRequest struct {
	ClawID   string  `json:"claw_id"`
	LimitUSD float64 `json:"limit_usd"`
	Window   string  `json:"window"`
	Behavior string  `json:"behavior"`
}

type modelRestrictRequest struct {
	ClawID        string   `json:"claw_id"`
	AllowedModels []string `json:"allowed_models"`
}

var knownBudgetBehaviors = map[string]bool{
	"rate_limit":      true,
	"hard_stop":       true,
	"soft_alert":      true,
	"graceful_switch": true,
}

func (h *apiHandler) handleRestart(w http.ResponseWriter, r *http.Request) {
	var req serviceTargetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Service = strings.TrimSpace(req.Service)
	if err := validateGovernanceTarget(req.Service); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	principal, ok := h.authorize(w, r, clawapi.VerbFleetRestart, req.Service)
	if !ok {
		return
	}
	_ = principal
	containers, err := h.listPodContainers(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	targets := matchServiceContainers(containers, req.Service)
	if len(targets) == 0 {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("no containers found for service %q", req.Service))
		return
	}
	for _, ctr := range targets {
		if err := h.docker.ContainerRestart(r.Context(), ctr.ID, containerapi.StopOptions{}); err != nil {
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("restart %s: %v", ctr.ID[:12], err))
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"restarted": req.Service, "count": len(targets)})
}

func (h *apiHandler) handleQuarantine(w http.ResponseWriter, r *http.Request) {
	var req serviceTargetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Service = strings.TrimSpace(req.Service)
	if err := validateGovernanceTarget(req.Service); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	principal, ok := h.authorize(w, r, clawapi.VerbFleetQuarantine, req.Service)
	if !ok {
		return
	}

	// Write quarantine marker first so the state is recorded even if the stop races.
	marker := map[string]any{
		"quarantined": true,
		"at":          time.Now().UTC().Format(time.RFC3339),
		"by":          principal.Name,
	}
	if err := h.writeGovernanceFile(req.Service, "quarantine.json", marker); err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("write quarantine marker: %v", err))
		return
	}

	if h.docker == nil {
		writeJSONError(w, http.StatusInternalServerError, "docker client unavailable")
		return
	}
	containers, err := h.listPodContainers(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	targets := matchServiceContainers(containers, req.Service)
	if len(targets) == 0 {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("no containers found for service %q", req.Service))
		return
	}
	for _, ctr := range targets {
		if err := h.docker.ContainerStop(r.Context(), ctr.ID, containerapi.StopOptions{}); err != nil {
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("stop %s: %v", ctr.ID[:12], err))
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"quarantined": req.Service, "count": len(targets)})
}

func (h *apiHandler) handleBudgetSet(w http.ResponseWriter, r *http.Request) {
	var req budgetSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.ClawID = strings.TrimSpace(req.ClawID)
	if err := validateGovernanceTarget(req.ClawID); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.LimitUSD <= 0 {
		writeJSONError(w, http.StatusBadRequest, "limit_usd must be positive")
		return
	}
	if req.Window == "" {
		writeJSONError(w, http.StatusBadRequest, "missing window")
		return
	}
	if !knownBudgetBehaviors[req.Behavior] {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("unknown behavior %q: must be one of rate_limit, hard_stop, soft_alert, graceful_switch", req.Behavior))
		return
	}
	_, ok := h.authorize(w, r, clawapi.VerbFleetBudgetSet, req.ClawID)
	if !ok {
		return
	}
	payload := map[string]any{
		"limit_usd": req.LimitUSD,
		"window":    req.Window,
		"behavior":  req.Behavior,
	}
	if err := h.writeGovernanceFile(req.ClawID, "budget.json", payload); err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("write budget override: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"claw_id": req.ClawID, "budget": payload})
}

func (h *apiHandler) handleModelRestrict(w http.ResponseWriter, r *http.Request) {
	var req modelRestrictRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.ClawID = strings.TrimSpace(req.ClawID)
	if err := validateGovernanceTarget(req.ClawID); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.AllowedModels) == 0 {
		writeJSONError(w, http.StatusBadRequest, "allowed_models must not be empty")
		return
	}
	_, ok := h.authorize(w, r, clawapi.VerbFleetModelRestrict, req.ClawID)
	if !ok {
		return
	}
	payload := map[string]any{
		"allowed_models": req.AllowedModels,
	}
	if err := h.writeGovernanceFile(req.ClawID, "model-restrict.json", payload); err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("write model-restrict override: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"claw_id": req.ClawID, "model_restrict": payload})
}

// writeGovernanceFile atomically writes a JSON file to <governanceDir>/<target>/<name>.
// target must be a single path component (no slashes, no traversal).
func (h *apiHandler) writeGovernanceFile(target, name string, payload any) error {
	if err := validateGovernanceTarget(target); err != nil {
		return err
	}
	dir := filepath.Join(h.governanceDir, target)
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return err
	}
	dest := filepath.Join(dir, name)
	tmp := dest + ".tmp"
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o666); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

// validateGovernanceTarget rejects targets that could escape the governance directory.
// A valid target is a single path component that does not start with a dot and
// contains no path separators or traversal sequences.
func validateGovernanceTarget(target string) error {
	if target == "" {
		return fmt.Errorf("governance target must not be empty")
	}
	if strings.HasPrefix(target, ".") {
		return fmt.Errorf("governance target %q must not start with '.'", target)
	}
	if filepath.Base(target) != target {
		return fmt.Errorf("governance target %q must be a single path component", target)
	}
	if strings.Contains(target, "/") || strings.Contains(target, "\\") {
		return fmt.Errorf("governance target %q contains invalid path separator", target)
	}
	return nil
}

func (h *apiHandler) listPodContainers(ctx context.Context) ([]types.Container, error) {
	args := filters.NewArgs(filters.Arg("label", "claw.pod="+h.manifest.PodName))
	return h.docker.ContainerList(ctx, containerapi.ListOptions{All: true, Filters: args})
}

func (h *apiHandler) logDecision(principal, verb, target string, allowed bool, detail string) {
	if err := h.auditLog.Encode(map[string]any{
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"type":      "claw_api_audit",
		"principal": principal,
		"verb":      verb,
		"target":    target,
		"allowed":   allowed,
		"detail":    detail,
	}); err != nil && h.auditErr != nil {
		fmt.Fprintf(h.auditErr, "claw-api audit logging failed: %v\n", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func parseLinesArg(raw string, fallback int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("invalid lines value %q: must be a positive integer", raw)
	}
	if value > 1000 {
		return 1000, nil
	}
	return value, nil
}

func parseSinceQuery(raw string, fallback time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	if dur, err := time.ParseDuration(raw); err == nil {
		return time.Now().Add(-dur), nil
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts, nil
	}
	return time.Time{}, fmt.Errorf("invalid since value %q: use duration or RFC3339", raw)
}

func composeServiceName(container types.Container) string {
	if name := container.Labels["com.docker.compose.service"]; name != "" {
		return name
	}
	if len(container.Names) > 0 {
		return strings.TrimPrefix(container.Names[0], "/")
	}
	return container.ID[:12]
}

func matchServiceContainers(containers []types.Container, service string) []types.Container {
	matched := make([]types.Container, 0)
	for _, ctr := range containers {
		if ctr.Labels["claw.service"] == service || composeServiceName(ctr) == service {
			matched = append(matched, ctr)
		}
	}
	return matched
}

func containerHealth(info types.ContainerJSON) (string, string) {
	if info.State == nil {
		return "unknown", "state unavailable"
	}
	if info.State.Health != nil && info.State.Health.Status != "" {
		return info.State.Health.Status, "native docker healthcheck"
	}
	if info.State.Running {
		return "running", "running"
	}
	if info.State.Status != "" {
		return info.State.Status, info.State.Status
	}
	return "unknown", "state unavailable"
}

func mergeStatus(currentStatus, currentDetail, nextStatus, nextDetail string) (string, string) {
	rank := map[string]int{
		"healthy":   0,
		"running":   1,
		"degraded":  2,
		"unhealthy": 3,
		"exited":    4,
		"missing":   5,
		"error":     6,
	}
	if currentStatus == "" || rank[nextStatus] > rank[currentStatus] {
		return nextStatus, nextDetail
	}
	return currentStatus, currentDetail
}

func startedAgo(raw string) string {
	startedAt, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return ""
	}
	return time.Since(startedAt).Round(time.Second).String()
}
