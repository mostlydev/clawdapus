package main

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	containerapi "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	manifestpkg "github.com/mostlydev/clawdapus/internal/clawdash"
	"github.com/mostlydev/clawdapus/internal/cllama"
	"github.com/mostlydev/clawdapus/internal/driver"
)

//go:embed templates/*.html
var templateFS embed.FS

type statusSource interface {
	Snapshot(ctx context.Context, serviceNames []string) (map[string]serviceStatus, error)
}

type handler struct {
	manifest        *manifestpkg.PodManifest
	statusSource    statusSource
	cllamaCostsURL  string
	costLogFallback bool
	httpClient      *http.Client
	tpl             *template.Template
}

func newHandler(manifest *manifestpkg.PodManifest, source statusSource, cllamaCostsURL string, costLogFallback bool) http.Handler {
	funcs := template.FuncMap{
		"statusClass":   statusClass,
		"pathEscape":    url.PathEscape,
		"join":          strings.Join,
		"title":         strings.Title, //nolint:staticcheck // simple title-case for badges.
		"truncate":      truncate,
		"statusLabel":   statusLabel,
		"hasStatusData": hasStatusData,
	}
	tpl := template.Must(template.New("clawdash").Funcs(funcs).ParseFS(templateFS, "templates/*.html"))
	return &handler{
		manifest:        manifest,
		statusSource:    source,
		cllamaCostsURL:  strings.TrimSpace(cllamaCostsURL),
		costLogFallback: costLogFallback,
		httpClient: &http.Client{
			Timeout: 2 * time.Second,
		},
		tpl: tpl,
	}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/":
		h.renderFleet(w, r)
		return
	case r.Method == http.MethodGet && r.URL.Path == "/topology":
		h.renderTopology(w, r)
		return
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/detail/"):
		h.renderDetail(w, r)
		return
	case r.Method == http.MethodGet && r.URL.Path == "/api/status":
		h.renderAPIStatus(w, r)
		return
	case r.Method == http.MethodGet && r.URL.Path == "/healthz":
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	default:
		http.NotFound(w, r)
		return
	}
}

type fleetPageData struct {
	PodName         string
	ActiveTab       string
	Agents          []fleetCard
	Proxies         []fleetCard
	Infrastructure  []fleetCard
	HasCllama       bool
	CllamaCostsURL  string
	HasCostLink     bool
	HasCostSummary  bool
	CostSummary     cllamaCostSummary
	CostSummaryErr  string
	StatusError     string
	HasStatusErrors bool
}

type cllamaCostSummary struct {
	TotalCostUSD float64
	Requests     int
	ProxyCount   int
	Source       string
}

type fleetCard struct {
	ServiceName  string
	DetailPath   string
	RoleBadge    string
	RoleClass    string
	ClawType     string
	Status       string
	StatusClass  string
	Uptime       string
	Model        string
	Handles      []handleRow
	ProxyType    string
	Count        int
	RunningCount int
}

type handleRow struct {
	Platform string
	Username string
}

func (h *handler) renderFleet(w http.ResponseWriter, r *http.Request) {
	statuses, statusErr := h.snapshot(r.Context())
	data := h.buildFleetPageData(r.Context(), statuses, statusErr)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = h.tpl.ExecuteTemplate(w, "fleet.html", data)
}

func (h *handler) buildFleetPageData(ctx context.Context, statuses map[string]serviceStatus, statusErr string) fleetPageData {
	serviceNames := sortedServiceNames(h.manifest.Services)
	proxyByService := make(map[string]manifestpkg.ProxyManifest, len(h.manifest.Proxies))
	for _, p := range h.manifest.Proxies {
		proxyByService[p.ServiceName] = p
	}

	agents := make([]fleetCard, 0)
	infra := make([]fleetCard, 0)
	for _, name := range serviceNames {
		svc := h.manifest.Services[name]
		status := statuses[name]
		card := fleetCard{
			ServiceName:  name,
			DetailPath:   "/detail/" + url.PathEscape(name),
			Status:       status.Status,
			StatusClass:  statusClass(status.Status),
			Uptime:       status.Uptime,
			Model:        primaryModel(svc.Models),
			Handles:      sortedHandles(svc.Handles),
			Count:        svc.Count,
			RunningCount: status.Running,
		}
		if card.Count < 1 {
			card.Count = 1
		}

		if svc.ClawType != "" {
			card.RoleBadge = svc.ClawType
			card.RoleClass = "badge-cyan"
			card.ClawType = svc.ClawType
			card.ProxyType = joinNonEmpty(svc.Cllama, ", ")
			agents = append(agents, card)
			continue
		}

		if proxy, ok := proxyByService[name]; ok {
			card.RoleBadge = "proxy"
			card.RoleClass = "badge-amber"
			card.ProxyType = proxy.ProxyType
			agents = append(agents, card)
			continue
		}

		card.RoleBadge = "native"
		card.RoleClass = "badge-green"
		infra = append(infra, card)
	}

	proxies := make([]fleetCard, 0, len(h.manifest.Proxies))
	for _, proxy := range h.manifest.Proxies {
		status := statuses[proxy.ServiceName]
		proxies = append(proxies, fleetCard{
			ServiceName: proxy.ServiceName,
			DetailPath:  "/detail/" + url.PathEscape(proxy.ServiceName),
			RoleBadge:   "proxy",
			RoleClass:   "badge-amber",
			Status:      status.Status,
			StatusClass: statusClass(status.Status),
			Uptime:      status.Uptime,
			ProxyType:   proxy.ProxyType,
			Count:       1,
		})
	}
	sort.Slice(proxies, func(i, j int) bool { return proxies[i].ServiceName < proxies[j].ServiceName })

	costSummary, costErr := h.fetchCllamaCostSummary(ctx)

	return fleetPageData{
		PodName:         h.manifest.PodName,
		ActiveTab:       "fleet",
		Agents:          agents,
		Proxies:         proxies,
		Infrastructure:  infra,
		HasCllama:       len(proxies) > 0,
		CllamaCostsURL:  h.cllamaCostsURL,
		HasCostLink:     costSummary != nil && costSummary.Source == "api" && strings.TrimSpace(h.cllamaCostsURL) != "",
		HasCostSummary:  costSummary != nil,
		CostSummary:     firstCostSummary(costSummary),
		CostSummaryErr:  costErr,
		StatusError:     statusErr,
		HasStatusErrors: statusErr != "",
	}
}

type detailPageData struct {
	PodName         string
	ActiveTab       string
	ServiceName     string
	ImageRef        string
	Count           int
	IsProxy         bool
	Status          serviceStatus
	StatusClass     string
	StatusError     string
	Surfaces        []manifestpkg.SurfaceManifest
	Handles         []handleDetailRow
	Skills          []string
	Invocations     []driver.Invocation
	Models          []modelRow
	Cllama          []cllamaDetailRow
	HasStatusErrors bool
}

type handleDetailRow struct {
	Platform string
	Username string
	ID       string
	Guilds   []driver.GuildInfo
}

type modelRow struct {
	Slot  string
	Model string
}

type cllamaDetailRow struct {
	ProxyType   string
	ServiceName string
	TokenStatus string
}

func (h *handler) renderDetail(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimPrefix(r.URL.Path, "/detail/")
	name, err := url.PathUnescape(raw)
	if err != nil || strings.TrimSpace(name) == "" {
		http.NotFound(w, r)
		return
	}

	statuses, statusErr := h.snapshot(r.Context())
	data, ok := h.buildDetailPageData(name, statuses, statusErr)
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = h.tpl.ExecuteTemplate(w, "detail.html", data)
}

func (h *handler) buildDetailPageData(name string, statuses map[string]serviceStatus, statusErr string) (detailPageData, bool) {
	svc, ok := h.manifest.Services[name]
	proxyInfo, isProxy := h.proxyByServiceName(name)
	if !ok && !isProxy {
		return detailPageData{}, false
	}

	if !ok && isProxy {
		svc = manifestpkg.ServiceManifest{
			ImageRef: proxyInfo.Image,
			Count:    1,
		}
	}
	if svc.Count < 1 {
		svc.Count = 1
	}

	models := make([]modelRow, 0, len(svc.Models))
	for slot, modelRef := range svc.Models {
		models = append(models, modelRow{Slot: slot, Model: modelRef})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Slot < models[j].Slot })

	handleRows := make([]handleDetailRow, 0, len(svc.Handles))
	for platform, info := range svc.Handles {
		if info == nil {
			continue
		}
		handleRows = append(handleRows, handleDetailRow{
			Platform: platform,
			Username: info.Username,
			ID:       info.ID,
			Guilds:   info.Guilds,
		})
	}
	sort.Slice(handleRows, func(i, j int) bool { return handleRows[i].Platform < handleRows[j].Platform })

	cllamaRows := make([]cllamaDetailRow, 0)
	proxyByType := make(map[string]string, len(h.manifest.Proxies))
	for _, p := range h.manifest.Proxies {
		proxyByType[p.ProxyType] = p.ServiceName
	}
	tokenStatus := "absent"
	if statuses[name].HasCllamaToken {
		tokenStatus = "present"
	}
	for _, proxyType := range svc.Cllama {
		serviceName := proxyByType[proxyType]
		if serviceName == "" {
			serviceName = cllama.ProxyServiceName(proxyType)
		}
		cllamaRows = append(cllamaRows, cllamaDetailRow{
			ProxyType:   proxyType,
			ServiceName: serviceName,
			TokenStatus: tokenStatus,
		})
	}
	if isProxy {
		cllamaRows = append(cllamaRows, cllamaDetailRow{
			ProxyType:   proxyInfo.ProxyType,
			ServiceName: proxyInfo.ServiceName,
			TokenStatus: "absent",
		})
	}

	status := statuses[name]
	if status.Service == "" {
		status = unknownStatus(name)
	}

	return detailPageData{
		PodName:         h.manifest.PodName,
		ActiveTab:       "detail",
		ServiceName:     name,
		ImageRef:        firstNonEmpty(svc.ImageRef, proxyInfo.Image),
		Count:           svc.Count,
		IsProxy:         isProxy,
		Status:          status,
		StatusClass:     statusClass(status.Status),
		StatusError:     statusErr,
		Surfaces:        svc.Surfaces,
		Handles:         handleRows,
		Skills:          slices.Clone(svc.Skills),
		Invocations:     slices.Clone(svc.Invocations),
		Models:          models,
		Cllama:          cllamaRows,
		HasStatusErrors: statusErr != "",
	}, true
}

func (h *handler) renderTopology(w http.ResponseWriter, r *http.Request) {
	statuses, statusErr := h.snapshot(r.Context())
	data := buildTopologyPageData(h.manifest, statuses, statusErr)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = h.tpl.ExecuteTemplate(w, "topology.html", data)
}

type apiStatusResponse struct {
	GeneratedAt string                   `json:"generatedAt"`
	Services    map[string]serviceStatus `json:"services"`
	Error       string                   `json:"error,omitempty"`
}

func (h *handler) renderAPIStatus(w http.ResponseWriter, r *http.Request) {
	statuses, err := h.snapshot(r.Context())
	resp := apiStatusResponse{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Services:    statuses,
	}
	code := http.StatusOK
	if err != "" {
		resp.Error = err
		code = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *handler) snapshot(ctx context.Context) (map[string]serviceStatus, string) {
	names := h.allServiceNames()
	timeoutCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	statuses, err := h.statusSource.Snapshot(timeoutCtx, names)
	if err == nil {
		return statuses, ""
	}
	fallback := make(map[string]serviceStatus, len(names))
	for _, name := range names {
		fallback[name] = unknownStatus(name)
	}
	return fallback, fmt.Sprintf("live status unavailable: %v", err)
}

func (h *handler) allServiceNames() []string {
	set := make(map[string]struct{}, len(h.manifest.Services)+len(h.manifest.Proxies))
	for name := range h.manifest.Services {
		set[name] = struct{}{}
	}
	for _, proxy := range h.manifest.Proxies {
		if strings.TrimSpace(proxy.ServiceName) != "" {
			set[proxy.ServiceName] = struct{}{}
		}
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (h *handler) proxyByServiceName(name string) (manifestpkg.ProxyManifest, bool) {
	for _, proxy := range h.manifest.Proxies {
		if proxy.ServiceName == name {
			return proxy, true
		}
	}
	return manifestpkg.ProxyManifest{}, false
}

func readManifest(path string) (*manifestpkg.PodManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var manifest manifestpkg.PodManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, err
	}
	if manifest.Services == nil {
		manifest.Services = make(map[string]manifestpkg.ServiceManifest)
	}
	return &manifest, nil
}

func sortedServiceNames(services map[string]manifestpkg.ServiceManifest) []string {
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedHandles(handles map[string]*driver.HandleInfo) []handleRow {
	out := make([]handleRow, 0, len(handles))
	for platform, info := range handles {
		if info == nil {
			continue
		}
		username := info.Username
		if strings.TrimSpace(username) == "" {
			username = info.ID
		}
		out = append(out, handleRow{
			Platform: platform,
			Username: username,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Platform < out[j].Platform })
	return out
}

func primaryModel(models map[string]string) string {
	if len(models) == 0 {
		return ""
	}
	if primary := strings.TrimSpace(models["primary"]); primary != "" {
		return primary
	}
	keys := make([]string, 0, len(models))
	for k := range models {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.TrimSpace(models[k]) != "" {
			return models[k]
		}
	}
	return ""
}

func statusClass(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "healthy", "running":
		return "status-healthy"
	case "starting":
		return "status-starting"
	case "unhealthy", "stopped", "dead", "exited":
		return "status-unhealthy"
	default:
		return "status-unknown"
	}
}

func statusLabel(status string) string {
	s := strings.TrimSpace(status)
	if s == "" {
		return "unknown"
	}
	return s
}

func hasStatusData(value string) bool {
	return strings.TrimSpace(value) != ""
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func joinNonEmpty(items []string, sep string) string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	return strings.Join(out, sep)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstCostSummary(in *cllamaCostSummary) cllamaCostSummary {
	if in == nil {
		return cllamaCostSummary{}
	}
	return *in
}

func (h *handler) fetchCllamaCostSummary(ctx context.Context) (*cllamaCostSummary, string) {
	if len(h.manifest.Proxies) == 0 {
		return nil, ""
	}
	summary, apiErr := h.fetchCllamaCostSummaryFromAPI(ctx)
	if summary != nil {
		summary.Source = "api"
		return summary, ""
	}
	if !h.costLogFallback {
		return nil, apiErr
	}
	if summary, err := h.fetchCllamaCostSummaryFromLogs(ctx); summary != nil {
		summary.Source = "logs"
		if strings.TrimSpace(apiErr) != "" {
			return summary, fmt.Sprintf("cost API unavailable (%s); showing log-derived estimate", apiErr)
		}
		if strings.TrimSpace(err) != "" {
			return summary, err
		}
		return summary, "showing log-derived estimate"
	}
	if strings.TrimSpace(apiErr) != "" {
		return nil, apiErr
	}
	return nil, "no cllama cost data available"
}

func (h *handler) fetchCllamaCostSummaryFromAPI(ctx context.Context) (*cllamaCostSummary, string) {
	summary := &cllamaCostSummary{}
	success := 0
	lastErr := ""

	for _, proxy := range h.manifest.Proxies {
		serviceName := strings.TrimSpace(proxy.ServiceName)
		if serviceName == "" {
			continue
		}
		endpoint := fmt.Sprintf("http://%s:8081/costs/api", serviceName)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			lastErr = fmt.Sprintf("build request for %s: %v", serviceName, err)
			continue
		}

		resp, err := h.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Sprintf("%s unavailable: %v", serviceName, err)
			continue
		}

		var payload map[string]interface{}
		if resp.StatusCode == http.StatusOK {
			if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
				lastErr = fmt.Sprintf("%s invalid JSON from /costs/api", serviceName)
				_ = resp.Body.Close()
				continue
			}
		} else {
			lastErr = fmt.Sprintf("%s missing /costs/api (status %d)", serviceName, resp.StatusCode)
			_ = resp.Body.Close()
			continue
		}
		_ = resp.Body.Close()

		summary.TotalCostUSD += asFloat(payload["total_cost_usd"])
		summary.Requests += asInt(payload["total_requests"])
		success++
	}

	if success == 0 {
		if strings.TrimSpace(lastErr) == "" {
			lastErr = "no cllama cost emission endpoint detected"
		}
		return nil, lastErr
	}
	summary.ProxyCount = success
	return summary, ""
}

func (h *handler) fetchCllamaCostSummaryFromLogs(ctx context.Context) (*cllamaCostSummary, string) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Sprintf("docker client unavailable for cost log fallback: %v", err)
	}
	defer cli.Close()

	summary := &cllamaCostSummary{}
	success := 0
	lastErr := ""

	for _, proxy := range h.manifest.Proxies {
		serviceName := strings.TrimSpace(proxy.ServiceName)
		if serviceName == "" {
			continue
		}

		containerID, err := findProxyContainerID(ctx, cli, h.manifest.PodName, serviceName)
		if err != nil {
			lastErr = fmt.Sprintf("%s container lookup failed: %v", serviceName, err)
			continue
		}

		rc, err := cli.ContainerLogs(ctx, containerID, containerapi.LogsOptions{
			ShowStdout: true,
			ShowStderr: false,
			Tail:       "500",
		})
		if err != nil {
			lastErr = fmt.Sprintf("%s log read failed: %v", serviceName, err)
			continue
		}

		var stdout bytes.Buffer
		var stderr bytes.Buffer
		_, copyErr := stdcopy.StdCopy(&stdout, &stderr, rc)
		_ = rc.Close()
		if copyErr != nil && copyErr != io.EOF {
			lastErr = fmt.Sprintf("%s log decode failed: %v", serviceName, copyErr)
			continue
		}

		total, reqs := parseCostSummaryFromLogs(stdout.String())
		summary.TotalCostUSD += total
		summary.Requests += reqs
		success++
	}

	if success == 0 {
		if strings.TrimSpace(lastErr) == "" {
			lastErr = "no proxy logs available for cost fallback"
		}
		return nil, lastErr
	}
	summary.ProxyCount = success
	return summary, ""
}

func findProxyContainerID(ctx context.Context, cli *client.Client, podName, serviceName string) (string, error) {
	args := filters.NewArgs(
		filters.Arg("label", "claw.pod="+strings.TrimSpace(podName)),
		filters.Arg("label", "claw.service="+strings.TrimSpace(serviceName)),
	)
	containers, err := cli.ContainerList(ctx, containerapi.ListOptions{
		All:     true,
		Filters: args,
	})
	if err != nil {
		return "", err
	}
	if len(containers) == 0 {
		return "", fmt.Errorf("not found")
	}
	return containers[0].ID, nil
}

func parseCostSummaryFromLogs(logs string) (float64, int) {
	total := 0.0
	requests := 0
	scanner := bufio.NewScanner(strings.NewReader(logs))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			continue
		}
		if _, ok := payload["cost_usd"]; !ok {
			continue
		}
		total += asFloat(payload["cost_usd"])
		requests++
	}
	return total, requests
}

func asFloat(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, err := n.Float64()
		if err == nil {
			return f
		}
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err == nil {
			return f
		}
	}
	return 0
}

func asInt(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case float32:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		i, err := n.Int64()
		if err == nil {
			return int(i)
		}
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		if err == nil {
			return i
		}
	}
	return 0
}
