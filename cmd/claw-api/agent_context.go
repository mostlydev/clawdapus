package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mostlydev/clawdapus/internal/clawapi"
)

const maxContextSnapshotBytes = 8 * 1024 * 1024

type agentIndexEntry struct {
	ClawID         string `json:"claw_id"`
	Service        string `json:"service,omitempty"`
	ClawType       string `json:"claw_type,omitempty"`
	HasLiveContext bool   `json:"has_live_context"`
}

type agentContractResponse struct {
	ClawID      string         `json:"claw_id"`
	AgentsMD    string         `json:"agents_md"`
	ClawdapusMD string         `json:"clawdapus_md"`
	Metadata    any            `json:"metadata"`
	Feeds       any            `json:"feeds"`
	Tools       any            `json:"tools"`
	Memory      any            `json:"memory"`
	ServiceAuth map[string]any `json:"service_auth,omitempty"`
}

type agentContextPath struct {
	AgentID string
	Action  string
}

func (h *apiHandler) handleAgentsList(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authorize(w, r, clawapi.VerbAgentContext, "")
	if !ok {
		return
	}
	agents, err := h.listContextAgents()
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	liveAgents := h.liveContextAgentSet(r.Context())
	out := make([]agentIndexEntry, 0, len(agents))
	for _, agent := range agents {
		if !h.allowsAgentContext(principal, agent.ClawID, agent.Service) {
			continue
		}
		agent.HasLiveContext = liveAgents[agent.ClawID]
		out = append(out, agent)
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
}

func (h *apiHandler) handleAgentDetail(w http.ResponseWriter, r *http.Request) {
	parsed, ok := parseAgentContextPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	principal, authorized := h.authorize(w, r, clawapi.VerbAgentContext, parsed.AgentID)
	if !authorized {
		return
	}
	agent, err := h.readContextAgent(parsed.AgentID)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSONError(w, http.StatusNotFound, "agent context not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !h.allowsAgentContext(principal, agent.ClawID, agent.Service) {
		h.logDecision(principal.Name, clawapi.VerbAgentContext, parsed.AgentID, false, "agent out of scope")
		writeJSONError(w, http.StatusForbidden, "agent is out of scope")
		return
	}

	switch parsed.Action {
	case "contract":
		h.handleAgentContract(w, parsed.AgentID)
	case "context":
		h.handleAgentLiveContext(w, r, parsed.AgentID)
	default:
		http.NotFound(w, r)
	}
}

func (h *apiHandler) handleAgentContract(w http.ResponseWriter, agentID string) {
	agentDir, err := h.agentContextDir(agentID)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	agentsMD, err := readAgentContractMD(agentDir)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	clawdapusMD, err := os.ReadFile(filepath.Join(agentDir, "CLAWDAPUS.md"))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("read CLAWDAPUS.md: %v", err))
		return
	}
	metadata, err := readJSONArtifact(filepath.Join(agentDir, "metadata.json"), false)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	feeds, err := readJSONArtifact(filepath.Join(agentDir, "feeds.json"), true)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tools, err := readJSONArtifact(filepath.Join(agentDir, "tools.json"), true)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	memory, err := readJSONArtifact(filepath.Join(agentDir, "memory.json"), true)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	serviceAuth, err := readServiceAuthArtifacts(filepath.Join(agentDir, "service-auth"))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, agentContractResponse{
		ClawID:      agentID,
		AgentsMD:    string(agentsMD),
		ClawdapusMD: string(clawdapusMD),
		Metadata:    redactJSONValue(metadata),
		Feeds:       redactJSONValue(feeds),
		Tools:       redactJSONValue(tools),
		Memory:      redactJSONValue(memory),
		ServiceAuth: redactServiceAuthArtifacts(serviceAuth),
	})
}

func readAgentContractMD(agentDir string) ([]byte, error) {
	effectivePath := filepath.Join(agentDir, "AGENTS.effective.md")
	data, err := os.ReadFile(effectivePath)
	if err == nil {
		return data, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read AGENTS.effective.md: %w", err)
	}

	data, err = os.ReadFile(filepath.Join(agentDir, "AGENTS.md"))
	if err != nil {
		return nil, fmt.Errorf("read AGENTS.md: %w", err)
	}
	return data, nil
}

func (h *apiHandler) handleAgentLiveContext(w http.ResponseWriter, r *http.Request, agentID string) {
	if strings.TrimSpace(h.cllamaAPIURL) == "" {
		writeJSONError(w, http.StatusServiceUnavailable, "cllama context API is not configured")
		return
	}
	target := strings.TrimRight(h.cllamaAPIURL, "/") + "/internal/context/" + url.PathEscape(agentID) + "/snapshot"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if h.cllamaAPIToken != "" {
		req.Header.Set("Authorization", "Bearer "+h.cllamaAPIToken)
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, fmt.Sprintf("cllama context request failed: %v", err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		writeJSONError(w, http.StatusNotFound, "no context captured yet")
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writeJSONError(w, http.StatusBadGateway, fmt.Sprintf("cllama context request returned %s", resp.Status))
		return
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, maxContextSnapshotBytes))
}

func (h *apiHandler) listContextAgents() ([]agentIndexEntry, error) {
	root := strings.TrimSpace(h.contextRoot)
	if root == "" {
		return nil, fmt.Errorf("agent context root is not configured")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read agent context root: %w", err)
	}
	agents := make([]agentIndexEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		agent, err := h.readContextAgent(entry.Name())
		if err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	sort.Slice(agents, func(i, j int) bool {
		return agents[i].ClawID < agents[j].ClawID
	})
	return agents, nil
}

func (h *apiHandler) readContextAgent(agentID string) (agentIndexEntry, error) {
	agentDir, err := h.agentContextDir(agentID)
	if err != nil {
		return agentIndexEntry{}, err
	}
	raw, err := os.ReadFile(filepath.Join(agentDir, "metadata.json"))
	if err != nil {
		return agentIndexEntry{}, err
	}
	var metadata map[string]any
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return agentIndexEntry{}, fmt.Errorf("parse metadata for %q: %w", agentID, err)
	}
	return agentIndexEntry{
		ClawID:   agentID,
		Service:  stringValue(metadata["service"]),
		ClawType: stringValue(metadata["type"]),
	}, nil
}

func (h *apiHandler) agentContextDir(agentID string) (string, error) {
	agentID = strings.TrimSpace(agentID)
	if err := validateGovernanceTarget(agentID); err != nil {
		return "", err
	}
	root := strings.TrimSpace(h.contextRoot)
	if root == "" {
		return "", fmt.Errorf("agent context root is not configured")
	}
	return filepath.Join(root, agentID), nil
}

func (h *apiHandler) allowsAgentContext(principal *clawapi.Principal, clawID, service string) bool {
	if principal == nil || h == nil || h.manifest == nil {
		return false
	}
	podName := h.manifest.PodName
	return principal.AllowsPod(podName) ||
		principal.AllowsClawID(podName, clawID) ||
		(service != "" && principal.AllowsService(podName, service))
}

func (h *apiHandler) liveContextAgentSet(ctx context.Context) map[string]bool {
	out := make(map[string]bool)
	if strings.TrimSpace(h.cllamaAPIURL) == "" {
		return out
	}
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, strings.TrimRight(h.cllamaAPIURL, "/")+"/internal/context", nil)
	if err != nil {
		return out
	}
	if h.cllamaAPIToken != "" {
		req.Header.Set("Authorization", "Bearer "+h.cllamaAPIToken)
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return out
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out
	}
	var decoded struct {
		Agents []string `json:"agents"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&decoded); err != nil {
		return out
	}
	for _, agentID := range decoded.Agents {
		if strings.TrimSpace(agentID) != "" {
			out[agentID] = true
		}
	}
	return out
}

func parseAgentContextPath(path string) (agentContextPath, bool) {
	rest := strings.TrimPrefix(path, "/agents/")
	if rest == path || rest == "" {
		return agentContextPath{}, false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		return agentContextPath{}, false
	}
	agentID, err := url.PathUnescape(parts[0])
	if err != nil || strings.TrimSpace(agentID) == "" {
		return agentContextPath{}, false
	}
	action := strings.TrimSpace(parts[1])
	if action != "contract" && action != "context" {
		return agentContextPath{}, false
	}
	return agentContextPath{AgentID: agentID, Action: action}, true
}

func readJSONArtifact(path string, optional bool) (any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if optional && os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	return out, nil
}

func readServiceAuthArtifacts(authDir string) (map[string]any, error) {
	entries, err := os.ReadDir(authDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read service-auth: %w", err)
	}
	out := make(map[string]any)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		parsed, err := readJSONArtifact(filepath.Join(authDir, entry.Name()), false)
		if err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		out[name] = parsed
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func redactServiceAuthArtifacts(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = redactJSONValue(v)
	}
	return out
}

func redactJSONValue(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, child := range typed {
			switch strings.ToLower(strings.TrimSpace(k)) {
			case "token", "secret":
				out[k] = "[REDACTED]"
			case "auth":
				out[k] = redactAuthValue(child)
			default:
				out[k] = redactJSONValue(child)
			}
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = redactJSONValue(child)
		}
		return out
	default:
		return v
	}
}

func redactAuthValue(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		out := map[string]any{}
		if authType := stringValue(typed["type"]); authType != "" {
			out["type"] = authType
		} else {
			out["type"] = "[REDACTED]"
		}
		return out
	case string:
		if typed == "" {
			return ""
		}
		return "[REDACTED]"
	default:
		if v == nil {
			return nil
		}
		return "[REDACTED]"
	}
}

func stringValue(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}
