package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/clawapi"
	manifestpkg "github.com/mostlydev/clawdapus/internal/clawdash"
)

func TestAgentsListFiltersByServiceScopeAndLiveContext(t *testing.T) {
	contextRoot := t.TempDir()
	writeAgentContextFixture(t, contextRoot, "trader-0", map[string]string{
		"service": "trader",
		"type":    "openclaw",
	})
	writeAgentContextFixture(t, contextRoot, "analyst-0", map[string]string{
		"service": "analyst",
		"type":    "nanobot",
	})

	var sawAuth string
	cllama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/internal/context" {
			t.Fatalf("unexpected cllama path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"agents":["trader-0"]}`))
	}))
	defer cllama.Close()

	h := newHandler(
		&manifestpkg.PodManifest{PodName: "ops"},
		nil,
		nil,
		nil,
		&clawapi.Store{Principals: []clawapi.Principal{{
			Name:     "trader-self",
			Token:    "capi_trader",
			Verbs:    []string{clawapi.VerbAgentContext},
			Services: []string{"trader"},
		}}},
		nil,
		nil,
		clawapi.DefaultThresholds(),
		t.TempDir(),
		withAgentContextConfig(contextRoot, cllama.URL, "ui-token"),
	)

	req := httptest.NewRequest(http.MethodGet, "/agents", nil)
	req.Header.Set("Authorization", "Bearer capi_trader")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if sawAuth != "Bearer ui-token" {
		t.Fatalf("expected cllama bearer token, got %q", sawAuth)
	}
	var resp struct {
		Agents []agentIndexEntry `json:"agents"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v body=%s", err, w.Body.String())
	}
	if len(resp.Agents) != 1 {
		t.Fatalf("expected 1 scoped agent, got %+v", resp.Agents)
	}
	got := resp.Agents[0]
	if got.ClawID != "trader-0" || got.Service != "trader" || got.ClawType != "openclaw" || !got.HasLiveContext {
		t.Fatalf("unexpected agent index entry: %+v", got)
	}
}

func TestAgentContractRedactsContextCredentials(t *testing.T) {
	contextRoot := t.TempDir()
	agentDir := writeAgentContextFixture(t, contextRoot, "trader-0", map[string]string{
		"service": "trader",
		"type":    "openclaw",
		"token":   "trader-0:secret",
	})
	if err := os.WriteFile(filepath.Join(agentDir, "feeds.json"), []byte(`[{"name":"alerts","auth":"feed-token"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "tools.json"), []byte(`{"tools":[{"name":"svc.tool","execution":{"auth":{"type":"bearer","token":"tool-token"}}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "memory.json"), []byte(`{"service":"mem","auth":{"type":"bearer","token":"memory-token"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "context-blocks.json"), []byte(`{"version":1,"blocks":[{"id":"focus","kind":"runtime_motivation","text":"Stay on the active operating contract.","enabled":true,"placement":"after_feeds","cadence":"every_turn","max_chars":800}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	authDir := filepath.Join(agentDir, "service-auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "claw-api.json"), []byte(`{"service":"claw-api","auth_type":"bearer","token":"api-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	h := newAgentContextTestHandler(t, contextRoot, clawapi.Principal{
		Name:  "dashboard",
		Token: "capi_dash",
		Verbs: []string{clawapi.VerbAgentContext},
		Pods:  []string{"ops"},
	})
	req := httptest.NewRequest(http.MethodGet, "/agents/trader-0/contract", nil)
	req.Header.Set("Authorization", "Bearer capi_dash")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "feed-token") || strings.Contains(w.Body.String(), "tool-token") || strings.Contains(w.Body.String(), "memory-token") || strings.Contains(w.Body.String(), "api-token") {
		t.Fatalf("contract response leaked credential: %s", w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	metadata := resp["metadata"].(map[string]any)
	if metadata["token"] != "[REDACTED]" {
		t.Fatalf("metadata token was not redacted: %+v", metadata)
	}
	feeds := resp["feeds"].([]any)
	if feeds[0].(map[string]any)["auth"] != "[REDACTED]" {
		t.Fatalf("feed auth was not redacted: %+v", feeds)
	}
	tools := resp["tools"].(map[string]any)
	tool := tools["tools"].([]any)[0].(map[string]any)
	execution := tool["execution"].(map[string]any)
	auth := execution["auth"].(map[string]any)
	if auth["type"] != "bearer" {
		t.Fatalf("tool auth type should be preserved without token, got %+v", auth)
	}
	if _, ok := auth["token"]; ok {
		t.Fatalf("tool auth token key should be removed, got %+v", auth)
	}
	serviceAuth := resp["service_auth"].(map[string]any)
	clawAPIAuth := serviceAuth["claw-api"].(map[string]any)
	if clawAPIAuth["token"] != "[REDACTED]" {
		t.Fatalf("service-auth token was not redacted: %+v", clawAPIAuth)
	}
	contextBlocks := resp["context_blocks"].(map[string]any)
	blocks := contextBlocks["blocks"].([]any)
	if blocks[0].(map[string]any)["id"] != "focus" || blocks[0].(map[string]any)["kind"] != "runtime_motivation" {
		t.Fatalf("context blocks were not returned: %+v", contextBlocks)
	}
}

func TestAgentContractPrefersEffectiveAgentsMD(t *testing.T) {
	contextRoot := t.TempDir()
	agentDir := writeAgentContextFixture(t, contextRoot, "worker-0", map[string]string{
		"service": "worker",
		"type":    "hermes",
	})
	if err := os.WriteFile(filepath.Join(agentDir, "AGENTS.effective.md"), []byte("# Effective Contract\n\ninfra included"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := newAgentContextTestHandler(t, contextRoot, clawapi.Principal{
		Name:  "dashboard",
		Token: "capi_dash",
		Verbs: []string{clawapi.VerbAgentContext},
		Pods:  []string{"ops"},
	})
	req := httptest.NewRequest(http.MethodGet, "/agents/worker-0/contract", nil)
	req.Header.Set("Authorization", "Bearer capi_dash")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp agentContractResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.AgentsMD != "# Effective Contract\n\ninfra included" {
		t.Fatalf("expected effective AGENTS.md, got %q", resp.AgentsMD)
	}
}

func TestAgentLiveContextProxiesCllamaSnapshot(t *testing.T) {
	contextRoot := t.TempDir()
	writeAgentContextFixture(t, contextRoot, "trader-0", map[string]string{
		"service": "trader",
		"type":    "openclaw",
	})
	var sawAuth string
	cllama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/internal/context/trader-0/snapshot" {
			t.Fatalf("unexpected cllama path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"agent_id":"trader-0","format":"openai","system":"ctx"}`))
	}))
	defer cllama.Close()

	h := newHandler(
		&manifestpkg.PodManifest{PodName: "ops"},
		nil,
		nil,
		nil,
		&clawapi.Store{Principals: []clawapi.Principal{{
			Name:  "dashboard",
			Token: "capi_dash",
			Verbs: []string{clawapi.VerbAgentContext},
			Pods:  []string{"ops"},
		}}},
		nil,
		nil,
		clawapi.DefaultThresholds(),
		t.TempDir(),
		withAgentContextConfig(contextRoot, cllama.URL, "ui-token"),
	)
	req := httptest.NewRequest(http.MethodGet, "/agents/trader-0/context", nil)
	req.Header.Set("Authorization", "Bearer capi_dash")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if sawAuth != "Bearer ui-token" {
		t.Fatalf("expected cllama bearer token, got %q", sawAuth)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal proxied response: %v", err)
	}
	if resp["agent_id"] != "trader-0" || resp["system"] != "ctx" {
		t.Fatalf("unexpected proxied snapshot: %+v", resp)
	}
}

func TestAgentContractRejectsOutOfScopeServicePrincipal(t *testing.T) {
	contextRoot := t.TempDir()
	writeAgentContextFixture(t, contextRoot, "trader-0", map[string]string{
		"service": "trader",
		"type":    "openclaw",
	})
	h := newAgentContextTestHandler(t, contextRoot, clawapi.Principal{
		Name:     "analyst-self",
		Token:    "capi_analyst",
		Verbs:    []string{clawapi.VerbAgentContext},
		Services: []string{"analyst"},
	})

	req := httptest.NewRequest(http.MethodGet, "/agents/trader-0/contract", nil)
	req.Header.Set("Authorization", "Bearer capi_analyst")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body.String())
	}
}

func newAgentContextTestHandler(t *testing.T, contextRoot string, principal clawapi.Principal) http.Handler {
	t.Helper()
	return newHandler(
		&manifestpkg.PodManifest{PodName: "ops"},
		nil,
		nil,
		nil,
		&clawapi.Store{Principals: []clawapi.Principal{principal}},
		nil,
		nil,
		clawapi.DefaultThresholds(),
		t.TempDir(),
		withAgentContextConfig(contextRoot, "", ""),
	)
}

func writeAgentContextFixture(t *testing.T, root, agentID string, metadata map[string]string) string {
	t.Helper()
	agentDir := filepath.Join(root, agentID)
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "AGENTS.md"), []byte("# Contract"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "CLAWDAPUS.md"), []byte("# Infra"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "metadata.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return agentDir
}
