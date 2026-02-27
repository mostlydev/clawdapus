# Phase 4: cllama Sidecar Integration — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Integrate the optional `cllama` governance proxy — a shared pod-level OpenAI-compatible proxy that intercepts all LLM traffic via credential starvation, with multi-provider support and a web UI for auth management.

**Architecture:** Two-repo effort. Repo 1 (`mostlydev/cllama-passthrough`) is a standalone Go binary — the reference proxy with transparent forwarding, multi-provider auth, web UI on :8081, and structured logging. Repo 2 (`mostlydev/clawdapus`) wires proxy services into compose lifecycle: detects `CLLAMA` directives (multiple per agent supported, chainable), injects typed proxy services (`cllama-<type>`), populates context dirs, rewires driver LLM config, and enforces credential starvation. The proxy is submoduled into clawdapus for E2E testing.

**Tech Stack:** Go 1.23, `net/http`, `encoding/json`, `crypto/rand`, `html/template` (embedded web UI), Docker multi-stage build

## Execution Status (2026-02-27)

- Slice 1 (`mostlydev/cllama-passthrough`): DONE
- Slices 2+3 (`mostlydev/clawdapus`): DONE
- Task 3.4 docs/ADRs: DONE
- Real-image spike (`TestSpikeComposeUp`): PASS
- Remaining planned future scope: policy pipeline + chain execution (Phase 5+)

---

## Key Design Decisions

| Decision | Resolution |
|----------|-----------|
| Proxy topology | Shared pod-level — one proxy **per type** per pod (e.g. `cllama-passthrough`, future `cllama-policy`). Multiple CLLAMA directives = multiple proxies, chainable. |
| API port | `:8080` — OpenAI-compatible `/v1/chat/completions` |
| UI port | `:8081` — operator web UI for provider management, separated from API |
| Agent key visibility | **NEVER** — agents never see real API keys, not even in env blocks |
| Provider auth config | `x-claw.cllama-env` block in pod YAML, injected only into proxy services (never agents). Mount for persistence + UI changes. Env overrides mount. Phase 4: single block shared by all proxies. Future: per-type env blocks. |
| Auth storage | `/claw/auth/providers.json` (mounted volume), env vars override |
| Multi-provider | Registry pattern: OpenAI, Anthropic, OpenRouter, Ollama (no-auth), extensible |
| Bearer token format | `<agent-id>:<hex-secret>` (48 hex chars from crypto/rand) |
| Context mount | `/claw/context/<agent-id>/` — AGENTS.md, CLAWDAPUS.md, metadata.json |
| compose_up refactor | Two-pass loop: pass 1 inspect+resolve, pass 2 materialize (enables pre-materialize token injection) |
| Per-ordinal identity | Count > 1 services get per-ordinal agent IDs (`svc-0`, `svc-1`), each with unique token and context dir. Materialize runs per-ordinal when cllama active. |
| Multi-proxy chaining | Multiple `CLLAMA` directives = multiple proxies chained in declaration order. Agent → first proxy → second proxy → ... → real provider. Phase 4 builds passthrough only; chain length is always 1. |
| CLLAMA data model | `Cllama []string` (not `string`). Clawfile: `CLLAMA passthrough`, `CLLAMA policy`. Pod YAML: `cllama: [passthrough, policy]` or `cllama: passthrough` (string coerced to single-element list). |
| Compose service naming | `cllama-<type>` (e.g. `cllama-passthrough`), not generic `cllama-proxy`. Each type gets its own service in the generated compose. |
| Mixed agent declarations | All cllama-enabled agents in a pod share the same proxy set. The union of all agents' CLLAMA lists determines which proxy services are injected. |
| Egress model | "No direct LLM egress" (credential starvation), NOT "no internet egress". Agents keep full internet access for Discord/Slack/APIs. Clarify in invariant docs. |
| Chain enforcement | Phase 4 **fails fast** if `len(Cllama) > 1` — multi-proxy chain contract is Phase 5 scope. Data model supports lists but runtime rejects chains. |
| Scope boundary | `require_cllama` (Slice 4), post-apply rollback, policy pipeline, and multi-proxy chain execution are explicitly OUT OF SCOPE for this plan. |

## Review-Driven Additions

Issues raised by two rounds of external architectural review (Codex xhigh 5.3). All addressed inline in tasks below.

### Round 1 Findings

1. **Per-ordinal identity (Critical):** When `count > 1`, each replica needs its own agent ID (`svc-0`, `svc-1`), unique bearer token, and unique context subdirectory. The current `expandedServiceNames` in compose_emit.go already generates ordinal suffixes. The cllama wiring must generate tokens and context dirs per-ordinal, not per-base-service. This affects Tasks 2.4, 2.8, and 3.2.

2. **CLLAMA_SPEC.md inconsistencies (High):** Section 4.D references `/claw/AGENTS.md` (legacy per-sidecar path); should be `/claw/context/<agent-id>/AGENTS.md`. Section 6 references `CLAW_ID` (legacy); should use bearer-token identity resolution. → Task 3.4 (doc fix).

3. **Credential starvation enforcement (Critical):** `stripLLMKeys` is necessary but not sufficient. Must also verify at preflight that no provider API keys exist in the pod YAML AND image-baked env vars for cllama-enabled agents. → Task 2.8 includes preflight via `docker inspect` env.

### Round 2 Findings (Codex xhigh detailed review)

4. **Secret validation (Critical):** Proxy must validate the hex secret from bearer token against stored `metadata.json` token field, not just parse the bearer format. → Task 1.5 handler validates secret against context metadata.

5. **Healthcheck incompatible with distroless (High):** Plan uses `wget` in compose healthcheck but distroless has no `wget`. → Fix: proxy binary exposes `/health` and compose healthcheck uses `CMD /cllama-passthrough -healthcheck` (binary self-check mode).

6. **Multi-proxy chaining incomplete (High):** Chain semantics (next-hop, identity propagation) are undefined. → Decision: Phase 4 supports `Cllama []string` data model but **fails fast if len > 1**. Chain contract is Phase 5 scope. Explicit error message if operator declares multiple proxies.

7. **Migration backward compat (High):** Indexed label parsing must sort by index (not map iteration order). Must support legacy `claw.cllama.default` label. `cmd/claw/inspect.go` also needs update. → Task 2.1 addresses all 8 touchpoints.

8. **Naming inconsistency (Medium):** Standardize on `cllama-env` (hyphenated) everywhere. YAML tag is `"cllama-env"`.

9. **Logging fields mismatch spec (Medium):** Logger must include `ts` (RFC3339), `claw_id`, `type`, `model`, `latency_ms`, `status_code`, `intervention` (nullable). → Task 1.6 updated.

10. **Missing doc/ADR tasks (Medium):** Add explicit tasks for CLAUDE.md update, CLLAMA_SPEC.md fixes, and ADR updates. → Task 3.4 (doc fixes).

---

## SLICE 1: cllama-passthrough reference (new repo: `mostlydev/cllama-passthrough`)

Slice 1 and Slices 2-3 are independent — can proceed in parallel.

### Task 1.1: Project scaffold + health endpoint

**Files:**
- Create: `go.mod` (module `github.com/mostlydev/cllama-passthrough`)
- Create: `cmd/cllama-passthrough/main.go`
- Create: `Dockerfile`

**Step 1: Create go.mod**

```
module github.com/mostlydev/cllama-passthrough

go 1.23
```

**Step 2: Write main.go**

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	apiAddr := envOr("LISTEN_ADDR", ":8080")
	uiAddr := envOr("UI_ADDR", ":8081")
	contextRoot := envOr("CLAW_CONTEXT_ROOT", "/claw/context")
	authDir := envOr("CLAW_AUTH_DIR", "/claw/auth")
	podName := os.Getenv("CLAW_POD")

	_ = contextRoot
	_ = authDir
	_ = podName

	// API server (OpenAI-compatible)
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true}`)
	})
	apiServer := &http.Server{Addr: apiAddr, Handler: apiMux}

	// UI server (operator management)
	uiMux := http.NewServeMux()
	uiMux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "cllama-passthrough UI (not yet implemented)")
	})
	uiServer := &http.Server{Addr: uiAddr, Handler: uiMux}

	go func() {
		log.Printf("cllama-passthrough API listening on %s", apiAddr)
		if err := apiServer.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("API server error: %v", err)
		}
	}()
	go func() {
		log.Printf("cllama-passthrough UI listening on %s", uiAddr)
		if err := uiServer.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("UI server error: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig

	ctx := context.Background()
	apiServer.Shutdown(ctx)
	uiServer.Shutdown(ctx)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
```

**Step 3: Write Dockerfile**

```dockerfile
FROM golang:1.23 AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download 2>/dev/null || true
COPY . .
RUN CGO_ENABLED=0 go build -o /cllama-passthrough ./cmd/cllama-passthrough

FROM gcr.io/distroless/static-debian12
COPY --from=build /cllama-passthrough /cllama-passthrough
EXPOSE 8080 8081
HEALTHCHECK --interval=15s --timeout=5s --retries=3 \
  CMD ["/cllama-passthrough", "-healthcheck"]
ENTRYPOINT ["/cllama-passthrough"]
```

Note: The binary supports `-healthcheck` flag which makes an HTTP GET to `http://localhost:8080/health` and exits 0/1. This works in distroless (no wget/curl needed). Add this to `main.go`:
```go
if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
	resp, err := http.Get("http://localhost:8080/health")
	if err != nil || resp.StatusCode != 200 {
		os.Exit(1)
	}
	os.Exit(0)
}
```

**Step 4: Verify**

Run: `go build ./cmd/cllama-passthrough`

**Step 5: Commit**

`feat(cllama): scaffold project with dual-server entrypoint and Dockerfile`

---

### Task 1.2: Bearer token parsing + identity resolution

**Files:**
- Create: `internal/identity/identity.go`
- Create: `internal/identity/identity_test.go`

**Step 1: Write the failing test**

```go
package identity

import "testing"

func TestParseBearerValid(t *testing.T) {
	id, secret, err := ParseBearer("Bearer tiverton:abc123def456")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "tiverton" {
		t.Errorf("expected id 'tiverton', got %q", id)
	}
	if secret != "abc123def456" {
		t.Errorf("expected secret 'abc123def456', got %q", secret)
	}
}

func TestParseBearerColonInSecret(t *testing.T) {
	id, secret, err := ParseBearer("Bearer bot-a:secret:with:colons")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "bot-a" {
		t.Errorf("expected id 'bot-a', got %q", id)
	}
	if secret != "secret:with:colons" {
		t.Errorf("expected secret 'secret:with:colons', got %q", secret)
	}
}

func TestParseBearerMissingColon(t *testing.T) {
	_, _, err := ParseBearer("Bearer noseparator")
	if err == nil {
		t.Error("expected error for missing colon")
	}
}

func TestParseBearerEmpty(t *testing.T) {
	_, _, err := ParseBearer("")
	if err == nil {
		t.Error("expected error for empty header")
	}
}

func TestParseBearerNoPrefixg(t *testing.T) {
	_, _, err := ParseBearer("Basic tiverton:abc")
	if err == nil {
		t.Error("expected error for non-Bearer auth")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/identity/ -v`
Expected: FAIL — `ParseBearer` not defined

**Step 3: Write implementation**

```go
package identity

import (
	"fmt"
	"strings"
)

// ParseBearer extracts agent ID and secret from "Bearer <agent-id>:<secret>".
// Splits on first colon only — secrets may contain colons.
func ParseBearer(header string) (agentID, secret string, err error) {
	if !strings.HasPrefix(header, "Bearer ") {
		return "", "", fmt.Errorf("invalid authorization: expected Bearer scheme")
	}
	token := strings.TrimPrefix(header, "Bearer ")
	parts := strings.SplitN(token, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid bearer token: expected <agent-id>:<secret>")
	}
	return parts[0], parts[1], nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/identity/ -v`
Expected: PASS

**Step 5: Commit**

`feat(cllama): bearer token parsing and identity resolution`

---

### Task 1.3: Provider registry (multi-provider auth)

**Files:**
- Create: `internal/provider/provider.go`
- Create: `internal/provider/provider_test.go`

**Step 1: Write the failing test**

```go
package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryFromEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test-openai")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")

	r := NewRegistry("")
	r.LoadFromEnv()

	p, err := r.Get("openai")
	if err != nil {
		t.Fatalf("openai: %v", err)
	}
	if p.APIKey != "sk-test-openai" {
		t.Errorf("expected openai key, got %q", p.APIKey)
	}
	if p.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("unexpected openai base URL: %q", p.BaseURL)
	}

	p, err = r.Get("anthropic")
	if err != nil {
		t.Fatalf("anthropic: %v", err)
	}
	if p.APIKey != "sk-ant-test" {
		t.Errorf("expected anthropic key, got %q", p.APIKey)
	}
}

func TestRegistryFromFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "providers.json")
	os.WriteFile(configPath, []byte(`{
		"providers": {
			"ollama": {"base_url": "http://ollama:11434/v1", "auth": "none"},
			"openrouter": {"base_url": "https://openrouter.ai/api/v1", "api_key": "sk-or-test"}
		}
	}`), 0644)

	r := NewRegistry(dir)
	if err := r.LoadFromFile(); err != nil {
		t.Fatalf("load from file: %v", err)
	}

	p, _ := r.Get("ollama")
	if p.BaseURL != "http://ollama:11434/v1" {
		t.Errorf("unexpected ollama URL: %q", p.BaseURL)
	}
	if p.Auth != "none" {
		t.Errorf("expected auth=none for ollama, got %q", p.Auth)
	}
}

func TestRegistryEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "providers.json")
	os.WriteFile(configPath, []byte(`{
		"providers": {
			"openai": {"base_url": "https://api.openai.com/v1", "api_key": "sk-from-file"}
		}
	}`), 0644)

	t.Setenv("OPENAI_API_KEY", "sk-from-env")

	r := NewRegistry(dir)
	r.LoadFromFile()
	r.LoadFromEnv() // env wins

	p, _ := r.Get("openai")
	if p.APIKey != "sk-from-env" {
		t.Errorf("env should override file, got %q", p.APIKey)
	}
}

func TestRegistryUnknownProvider(t *testing.T) {
	r := NewRegistry("")
	_, err := r.Get("nonexistent")
	if err == nil {
		t.Error("expected error for unknown provider")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/provider/ -v`

**Step 3: Write implementation**

```go
package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Provider holds auth config for one LLM provider.
type Provider struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key,omitempty"`
	Auth    string `json:"auth,omitempty"` // "bearer" (default), "none" (ollama), "oauth"
}

// Registry manages known LLM providers. Thread-safe.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]*Provider
	authDir   string
}

// Known provider defaults (base URLs).
var knownProviders = map[string]string{
	"openai":     "https://api.openai.com/v1",
	"anthropic":  "https://api.anthropic.com/v1",
	"openrouter": "https://openrouter.ai/api/v1",
}

// Env var → provider name mapping.
var envKeyMap = map[string]string{
	"OPENAI_API_KEY":     "openai",
	"ANTHROPIC_API_KEY":  "anthropic",
	"OPENROUTER_API_KEY": "openrouter",
}

func NewRegistry(authDir string) *Registry {
	return &Registry{
		providers: make(map[string]*Provider),
		authDir:   authDir,
	}
}

// LoadFromFile reads providers.json from the auth directory.
func (r *Registry) LoadFromFile() error {
	if r.authDir == "" {
		return nil
	}
	path := filepath.Join(r.authDir, "providers.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read providers.json: %w", err)
	}

	var cfg struct {
		Providers map[string]*Provider `json:"providers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse providers.json: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for name, p := range cfg.Providers {
		p.Name = name
		if p.Auth == "" {
			p.Auth = "bearer"
		}
		r.providers[name] = p
	}
	return nil
}

// LoadFromEnv scans environment for known API key patterns. Overrides file config.
func (r *Registry) LoadFromEnv() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for envKey, provName := range envKeyMap {
		val := os.Getenv(envKey)
		if val == "" {
			continue
		}
		existing, ok := r.providers[provName]
		if !ok {
			existing = &Provider{
				Name:    provName,
				BaseURL: knownProviders[provName],
				Auth:    "bearer",
			}
		}
		existing.APIKey = val
		r.providers[provName] = existing
	}
}

// Get returns a provider by name.
func (r *Registry) Get(name string) (*Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("unknown provider: %q", name)
	}
	return p, nil
}

// All returns all registered providers (sorted for determinism would be caller's job).
func (r *Registry) All() map[string]*Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]*Provider, len(r.providers))
	for k, v := range r.providers {
		out[k] = v
	}
	return out
}

// SaveToFile persists current providers to providers.json (for web UI updates).
func (r *Registry) SaveToFile() error {
	if r.authDir == "" {
		return fmt.Errorf("no auth directory configured")
	}
	r.mu.RLock()
	cfg := struct {
		Providers map[string]*Provider `json:"providers"`
	}{Providers: r.providers}
	r.mu.RUnlock()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(r.authDir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.authDir, "providers.json"), data, 0644)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/provider/ -v`

**Step 5: Commit**

`feat(cllama): multi-provider registry with file + env loading`

---

### Task 1.4: Context loading from mounted directory

**Files:**
- Create: `internal/agentctx/agentctx.go`
- Create: `internal/agentctx/agentctx_test.go`

**Step 1: Write the failing test**

```go
package agentctx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReadsAllFiles(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "tiverton")
	os.MkdirAll(agentDir, 0700)
	os.WriteFile(filepath.Join(agentDir, "AGENTS.md"), []byte("# Contract"), 0644)
	os.WriteFile(filepath.Join(agentDir, "CLAWDAPUS.md"), []byte("# Infra"), 0644)
	os.WriteFile(filepath.Join(agentDir, "metadata.json"), []byte(`{"service":"tiverton","pod":"ops"}`), 0644)

	ctx, err := Load(dir, "tiverton")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(ctx.AgentsMD) != "# Contract" {
		t.Errorf("wrong AGENTS.md content: %q", ctx.AgentsMD)
	}
	if string(ctx.ClawdapusMD) != "# Infra" {
		t.Errorf("wrong CLAWDAPUS.md content: %q", ctx.ClawdapusMD)
	}
	if ctx.Metadata["service"] != "tiverton" {
		t.Errorf("wrong metadata: %v", ctx.Metadata)
	}
}

func TestLoadMissingDirErrors(t *testing.T) {
	_, err := Load("/nonexistent", "ghost")
	if err == nil {
		t.Error("expected error for missing dir")
	}
}
```

**Step 2: Run test — expected FAIL**

**Step 3: Write implementation**

```go
package agentctx

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type AgentContext struct {
	AgentID     string
	AgentsMD    []byte
	ClawdapusMD []byte
	Metadata    map[string]interface{}
}

// Load reads an agent's context files from contextRoot/<agentID>/.
func Load(contextRoot, agentID string) (*AgentContext, error) {
	dir := filepath.Join(contextRoot, agentID)

	agentsMD, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		return nil, fmt.Errorf("load agent context %q: AGENTS.md: %w", agentID, err)
	}

	clawdapusMD, err := os.ReadFile(filepath.Join(dir, "CLAWDAPUS.md"))
	if err != nil {
		return nil, fmt.Errorf("load agent context %q: CLAWDAPUS.md: %w", agentID, err)
	}

	metaRaw, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		return nil, fmt.Errorf("load agent context %q: metadata.json: %w", agentID, err)
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		return nil, fmt.Errorf("load agent context %q: parse metadata: %w", agentID, err)
	}

	return &AgentContext{
		AgentID:     agentID,
		AgentsMD:    agentsMD,
		ClawdapusMD: clawdapusMD,
		Metadata:    meta,
	}, nil
}
```

**Step 4: Run test — expected PASS**

**Step 5: Commit**

`feat(cllama): context loading from mounted agent directory`

---

### Task 1.5: Transparent proxy handler

**Files:**
- Create: `internal/proxy/handler.go`
- Create: `internal/proxy/handler_test.go`

**Step 1: Write the failing test**

```go
package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mostlydev/cllama-passthrough/internal/provider"
)

func TestHandlerForwardsAndSwapsAuth(t *testing.T) {
	var gotAuth string
	var gotBody []byte
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"message":{"content":"hello"}}]}`))
	}))
	defer backend.Close()

	reg := provider.NewRegistry("")
	reg.Set("openai", &provider.Provider{
		Name: "openai", BaseURL: backend.URL, APIKey: "sk-real", Auth: "bearer",
	})

	// contextLoader returns a valid agent context for "tiverton"
	h := NewHandler(reg, stubContextLoader("tiverton"), io.Discard)
	body := `{"model":"openai/gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer tiverton:dummy123")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if gotAuth != "Bearer sk-real" {
		t.Errorf("expected real key forwarded, got %q", gotAuth)
	}
	if len(gotBody) == 0 {
		t.Error("backend received empty body")
	}
}

func TestHandlerRejectsMissingBearer(t *testing.T) {
	h := NewHandler(provider.NewRegistry(""), nil, io.Discard)
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHandlerRejectsUnknownProvider(t *testing.T) {
	reg := provider.NewRegistry("")
	h := NewHandler(reg, stubContextLoader("tiverton"), io.Discard)
	body := `{"model":"unknown-provider/model","messages":[]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer tiverton:dummy123")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 502 {
		t.Errorf("expected 502 for unknown provider, got %d", w.Code)
	}
}
```

**Step 2: Run test — expected FAIL**

**Step 3: Write implementation**

The handler:
1. Parses `Authorization` header via `identity.ParseBearer` → `(agentID, secret)`
2. Loads agent context via context loader → `AgentContext`
3. **Validates secret**: compares `secret` against `ctx.Metadata["token"]` — rejects with 403 if mismatch (prevents agent spoofing)
4. Reads request body, extracts `model` field (format: `provider/model-name`)
5. Looks up provider in registry
6. Builds outbound request: rewrites `Authorization` to real key, rewrites `model` to strip provider prefix
7. For `Auth: "none"` providers (ollama): omits Authorization header
8. Forwards to provider's `BaseURL + "/chat/completions"`
9. Streams response back via `io.Copy` with `http.Flusher` for SSE

Add test for secret mismatch:
```go
func TestHandlerRejectsWrongSecret(t *testing.T) {
	reg := provider.NewRegistry("")
	// Context has token "tiverton:correct-secret" but request sends wrong secret
	h := NewHandler(reg, stubContextLoaderWithToken("tiverton", "tiverton:correct-secret"), io.Discard)
	body := `{"model":"openai/gpt-4o","messages":[]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer tiverton:wrong-secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Errorf("expected 403 for wrong secret, got %d", w.Code)
	}
}
```

**Step 4: Run test — expected PASS**

**Step 5: Commit**

`feat(cllama): transparent proxy handler with credential swap and multi-provider routing`

---

### Task 1.6: Structured JSON logging

**Files:**
- Create: `internal/logging/logger.go`
- Create: `internal/logging/logger_test.go`

**Step 1: Write the failing test**

```go
package logging

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestLogRequestEmitsJSON(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf)
	l.LogRequest("tiverton", "openai/gpt-4o")

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, buf.String())
	}
	if entry["claw_id"] != "tiverton" {
		t.Errorf("expected claw_id=tiverton, got %v", entry["claw_id"])
	}
	if entry["type"] != "request" {
		t.Errorf("expected type=request, got %v", entry["type"])
	}
	if entry["model"] != "openai/gpt-4o" {
		t.Errorf("expected model, got %v", entry["model"])
	}
}

func TestLogResponseIncludesLatency(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf)
	l.LogResponse("tiverton", "openai/gpt-4o", 200, 1250)

	var entry map[string]interface{}
	json.Unmarshal(buf.Bytes(), &entry)
	if entry["type"] != "response" {
		t.Errorf("expected type=response")
	}
	if entry["latency_ms"].(float64) != 1250 {
		t.Errorf("expected latency_ms=1250, got %v", entry["latency_ms"])
	}
}
```

**Step 2: Run test — expected FAIL**

**Step 3: Implement** — `encoding/json` to `io.Writer`. Types: `request`, `response`, `error`. Fields: `ts` (RFC3339), `claw_id`, `type`, `model`, `latency_ms`, `status_code`.

**Step 4: Run test — expected PASS**

**Step 5: Commit**

`feat(cllama): structured JSON logging for request/response`

---

### Task 1.7: Web UI for provider management

**Files:**
- Create: `internal/ui/handler.go`
- Create: `internal/ui/handler_test.go`
- Create: `internal/ui/templates/` (embedded)

**Step 1: Write the failing test**

```go
package ui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mostlydev/cllama-passthrough/internal/provider"
)

func TestUIListsProviders(t *testing.T) {
	reg := provider.NewRegistry("")
	reg.Set("openai", &provider.Provider{Name: "openai", BaseURL: "https://api.openai.com/v1", APIKey: "sk-test", Auth: "bearer"})
	h := NewHandler(reg)
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("openai")) {
		t.Error("expected provider name in response")
	}
}
```

**Step 2-4:** Implement embedded HTML template that lists providers, shows masked keys, allows add/edit/delete. Form POSTs update the registry and call `SaveToFile()`. Minimal CSS — functional, not pretty.

**Step 5: Commit**

`feat(cllama): web UI for provider management on :8081`

---

### Task 1.8: Wire everything in main.go + integration smoke test

**Files:**
- Modify: `cmd/cllama-passthrough/main.go`
- Create: `cmd/cllama-passthrough/main_test.go` (integration)

Wire the provider registry, proxy handler, logger, context loader, and UI handler into main.go. Write a test that starts both servers and does a round-trip.

**Commit:** `feat(cllama): wire all components in main entrypoint`

---

## SLICE 2: Clawdapus infrastructure wiring (`mostlydev/clawdapus`)

### Task 2.1: Add `Cllama` and `CllamaToken` fields to ResolvedClaw + migrate existing Cllama to []string

**Files:**
- Modify: `internal/driver/types.go:21-37`
- Modify: `internal/clawfile/directives.go` — `Cllama string` → `Cllama []string`
- Modify: `internal/clawfile/parser.go:86-91` — accumulate multi-value (like INVOKE, not setSingleton)
- Modify: `internal/clawfile/emit.go:55-56` — emit indexed labels `claw.cllama.0`, `claw.cllama.1`
- Modify: `internal/inspect/inspect.go:23,70` — read indexed labels into `[]string`
- Modify: `internal/pod/types.go:32` — `Cllama string` → `Cllama []string` (yaml already handles string-or-list)
- Modify: `internal/pod/parser.go:43,132` — propagate `[]string`
- Modify: `cmd/claw/inspect.go` — display `[]string` Cllama field

**Step 1: No separate test — structural. Verified by Task 2.2 and existing tests.**

**Step 2: Add fields to ResolvedClaw**

```go
// After line 36 (Environment field), add:
Cllama      []string // cllama proxy types (e.g. ["passthrough"]). Empty = no proxy.
CllamaToken string   // per-agent bearer token for first cllama proxy in chain (set by compose_up)
```

**Step 3: Migrate Cllama from string to []string across codebase**

In `internal/clawfile/directives.go`:
```go
Cllama []string // was: string
```

In `internal/clawfile/parser.go` — change from `setSingleton` to append:
```go
case "cllama":
    if remainder == "" {
        return nil, fmt.Errorf("line %d: CLLAMA requires a value", node.StartLine)
    }
    config.Cllama = append(config.Cllama, remainder)
```

In `internal/clawfile/emit.go` — emit indexed labels:
```go
for i, c := range config.Cllama {
    lines = append(lines, formatLabel(fmt.Sprintf("claw.cllama.%d", i), c))
}
```

In `internal/inspect/inspect.go` — read indexed labels with sorting + legacy support:
```go
Cllama []string // was: string

// In parseLabels, collect into temporary map then sort by index:
// Support both indexed (claw.cllama.0, claw.cllama.1) and legacy (claw.cllama.default)
cllamaMap := make(map[int]string) // index → value
var cllamaMaxIdx int
for key, value := range labels {
    if key == "claw.cllama.default" {
        // Legacy: single proxy, treat as index 0
        cllamaMap[0] = value
    } else if strings.HasPrefix(key, "claw.cllama.") {
        suffix := strings.TrimPrefix(key, "claw.cllama.")
        idx, err := strconv.Atoi(suffix)
        if err == nil {
            cllamaMap[idx] = value
            if idx > cllamaMaxIdx { cllamaMaxIdx = idx }
        }
    }
}
// Build sorted slice
for i := 0; i <= cllamaMaxIdx; i++ {
    if v, ok := cllamaMap[i]; ok {
        info.Cllama = append(info.Cllama, v)
    }
}
```

In `internal/pod/types.go` and `internal/pod/parser.go` — YAML `cllama` field supports both `cllama: passthrough` (string) and `cllama: [passthrough, policy]` (list). Use `yaml.UnmarshalYAML` custom or `rawClawBlock.Cllama interface{}` with type switch to coerce string → `[]string`.

**Step 4: Run `go test ./...` to verify no regressions**

**Step 5: Commit**

`feat(cllama): migrate Cllama from string to []string for multi-proxy support`

---

### Task 2.2: Propagate Cllama into ResolvedClaw + detection helpers

**Files:**
- Modify: `cmd/claw/compose_up.go:188-203` (rc construction)
- Create test in: `cmd/claw/compose_up_test.go` (or add to existing)

**Step 1: Write the failing test**

```go
func TestResolveCllama(t *testing.T) {
	tests := []struct {
		name     string
		image    []string
		pod      []string
		want     []string
	}{
		{"pod overrides image", []string{"passthrough"}, []string{"passthrough", "policy"}, []string{"passthrough", "policy"}},
		{"image fallback", []string{"passthrough"}, nil, []string{"passthrough"}},
		{"both empty", nil, nil, nil},
		{"pod only", nil, []string{"passthrough"}, []string{"passthrough"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveCllama(tt.image, tt.pod)
			if !slices.Equal(got, tt.want) {
				t.Errorf("resolveCllama(%v, %v) = %v, want %v", tt.image, tt.pod, got, tt.want)
			}
		})
	}
}

func TestDetectCllama(t *testing.T) {
	claws := map[string]*driver.ResolvedClaw{
		"bot-a": {Cllama: nil},
		"bot-b": {Cllama: []string{"passthrough"}},
		"bot-c": {Cllama: []string{"passthrough", "policy"}},
	}
	enabled, agents := detectCllama(claws)
	if !enabled {
		t.Error("expected cllama enabled")
	}
	if len(agents) != 2 || agents[0] != "bot-b" || agents[1] != "bot-c" {
		t.Errorf("expected [bot-b bot-c], got %v", agents)
	}
}

func TestCollectProxyTypes(t *testing.T) {
	claws := map[string]*driver.ResolvedClaw{
		"bot-a": {Cllama: []string{"passthrough"}},
		"bot-b": {Cllama: []string{"passthrough", "policy"}},
	}
	types := collectProxyTypes(claws)
	// Union, sorted: [passthrough, policy]
	if !slices.Equal(types, []string{"passthrough", "policy"}) {
		t.Errorf("expected [passthrough policy], got %v", types)
	}
}
```

**Step 2: Run test — expected FAIL**

**Step 3: Implement helpers + wire into rc construction**

```go
// resolveCllama merges image-level and pod-level CLLAMA declarations.
// Pod-level takes full precedence when non-empty.
func resolveCllama(imageLevel, podLevel []string) []string {
	if len(podLevel) > 0 {
		return podLevel
	}
	return imageLevel
}

// detectCllama returns whether any agent has cllama enabled, and which agents.
func detectCllama(claws map[string]*driver.ResolvedClaw) (bool, []string) {
	var agents []string
	for name, rc := range claws {
		if len(rc.Cllama) > 0 {
			agents = append(agents, name)
		}
	}
	sort.Strings(agents)
	return len(agents) > 0, agents
}

// collectProxyTypes returns the sorted union of all proxy types across all agents.
func collectProxyTypes(claws map[string]*driver.ResolvedClaw) []string {
	seen := make(map[string]struct{})
	for _, rc := range claws {
		for _, t := range rc.Cllama {
			seen[t] = struct{}{}
		}
	}
	types := make([]string, 0, len(seen))
	for t := range seen {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
}
```

In rc construction (line 188), add: `Cllama: resolveCllama(info.Cllama, svc.Claw.Cllama),`

**Step 4: Run test — expected PASS**

**Step 5: Commit**

`feat(cllama): propagate Cllama []string into ResolvedClaw with detection helpers`

---

### Task 2.3: Bearer token generation (`internal/cllama/` package)

**Files:**
- Create: `internal/cllama/token.go`
- Create: `internal/cllama/token_test.go`

**Step 1: Write the failing test**

```go
package cllama

import (
	"strings"
	"testing"
)

func TestGenerateTokenFormat(t *testing.T) {
	tok := GenerateToken("tiverton")
	parts := strings.SplitN(tok, ":", 2)
	if parts[0] != "tiverton" {
		t.Errorf("expected agent-id prefix, got %q", parts[0])
	}
	if len(parts[1]) < 32 {
		t.Errorf("expected at least 32 char secret, got %d", len(parts[1]))
	}
}

func TestGenerateTokenUnique(t *testing.T) {
	a := GenerateToken("bot")
	b := GenerateToken("bot")
	if a == b {
		t.Error("tokens should be unique")
	}
}
```

**Step 2: Run test — expected FAIL**

**Step 3: Implement**

```go
package cllama

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func GenerateToken(agentID string) string {
	b := make([]byte, 24) // 48 hex chars
	rand.Read(b)
	return fmt.Sprintf("%s:%s", agentID, hex.EncodeToString(b))
}
```

**Step 4: Run test — expected PASS**

**Step 5: Commit**

`feat(cllama): per-agent bearer token generation`

---

### Task 2.4: Context directory generation

**Files:**
- Create: `internal/cllama/context.go`
- Create: `internal/cllama/context_test.go`

**Step 1: Write the failing test**

```go
package cllama

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateContextDirWritesFiles(t *testing.T) {
	dir := t.TempDir()
	agents := []AgentContextInput{{
		AgentID:     "tiverton",
		AgentsMD:    "# Contract",
		ClawdapusMD: "# Infra",
		Metadata:    map[string]interface{}{"service": "tiverton", "pod": "test-pod"},
	}}
	if err := GenerateContextDir(dir, agents); err != nil {
		t.Fatal(err)
	}

	agentsMD, _ := os.ReadFile(filepath.Join(dir, "context", "tiverton", "AGENTS.md"))
	if string(agentsMD) != "# Contract" {
		t.Errorf("wrong AGENTS.md: %q", agentsMD)
	}
	clawdapusMD, _ := os.ReadFile(filepath.Join(dir, "context", "tiverton", "CLAWDAPUS.md"))
	if string(clawdapusMD) != "# Infra" {
		t.Errorf("wrong CLAWDAPUS.md: %q", clawdapusMD)
	}
	metaRaw, _ := os.ReadFile(filepath.Join(dir, "context", "tiverton", "metadata.json"))
	var meta map[string]interface{}
	json.Unmarshal(metaRaw, &meta)
	if meta["service"] != "tiverton" {
		t.Errorf("wrong metadata: %v", meta)
	}
}

func TestGenerateContextDirMultipleAgents(t *testing.T) {
	dir := t.TempDir()
	agents := []AgentContextInput{
		{AgentID: "bot-a", AgentsMD: "# A", ClawdapusMD: "# A-infra", Metadata: map[string]interface{}{}},
		{AgentID: "bot-b", AgentsMD: "# B", ClawdapusMD: "# B-infra", Metadata: map[string]interface{}{}},
	}
	if err := GenerateContextDir(dir, agents); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "context", "bot-a", "AGENTS.md")); err != nil {
		t.Errorf("bot-a missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "context", "bot-b", "AGENTS.md")); err != nil {
		t.Errorf("bot-b missing: %v", err)
	}
}
```

**Step 2: Run test — expected FAIL**

**Step 3: Implement**

```go
package cllama

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type AgentContextInput struct {
	AgentID     string
	AgentsMD    string
	ClawdapusMD string
	Metadata    map[string]interface{}
}

func GenerateContextDir(runtimeDir string, agents []AgentContextInput) error {
	for _, agent := range agents {
		agentDir := filepath.Join(runtimeDir, "context", agent.AgentID)
		if err := os.MkdirAll(agentDir, 0700); err != nil {
			return fmt.Errorf("create context dir for %q: %w", agent.AgentID, err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "AGENTS.md"), []byte(agent.AgentsMD), 0644); err != nil {
			return fmt.Errorf("write AGENTS.md for %q: %w", agent.AgentID, err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "CLAWDAPUS.md"), []byte(agent.ClawdapusMD), 0644); err != nil {
			return fmt.Errorf("write CLAWDAPUS.md for %q: %w", agent.AgentID, err)
		}
		metaJSON, err := json.MarshalIndent(agent.Metadata, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal metadata for %q: %w", agent.AgentID, err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "metadata.json"), metaJSON, 0644); err != nil {
			return fmt.Errorf("write metadata.json for %q: %w", agent.AgentID, err)
		}
	}
	return nil
}
```

**Step 4: Run test — expected PASS**

**Step 5: Commit**

`feat(cllama): per-agent context directory generation`

---

### Task 2.5: Parse `x-claw.cllama.env` from pod YAML

**Files:**
- Modify: `internal/pod/parser.go` — add `CllamaEnv` to rawClawBlock + ClawBlock
- Modify: `internal/pod/types.go` — add `CllamaEnv map[string]string` to ClawBlock
- Test: `internal/pod/parser_test.go`

**Step 1: Write the failing test**

```go
func TestParseCllamaEnvBlock(t *testing.T) {
	yaml := `
name: test-pod
services:
  bot:
    image: bot:latest
    x-claw:
      cllama: passthrough
      cllama-env:
        OPENAI_API_KEY: sk-real-key
        ANTHROPIC_API_KEY: sk-ant-key
`
	p, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatal(err)
	}
	env := p.Services["bot"].Claw.CllamaEnv
	if env["OPENAI_API_KEY"] != "sk-real-key" {
		t.Errorf("expected OPENAI_API_KEY, got %v", env)
	}
}
```

**Step 2: Run test — expected FAIL**

**Step 3: Implement** — add `CllamaEnv map[string]string` to `rawClawBlock` with yaml tag `"cllama-env"`, propagate to `ClawBlock.CllamaEnv`.

**Step 4: Run test — expected PASS**

**Step 5: Commit**

`feat(cllama): parse x-claw.cllama-env from pod YAML`

---

### Task 2.6: Inject cllama proxy services into compose emission

**Files:**
- Modify: `internal/pod/compose_emit.go` — add `CllamaProxyConfig` type, modify `EmitCompose` signature
- Test: `internal/pod/compose_emit_test.go`

**Step 1: Write the failing test**

```go
func TestEmitComposeWithCllamaProxy(t *testing.T) {
	p := &Pod{Name: "test-pod", Services: map[string]*Service{
		"bot": {Image: "bot:latest", Claw: &ClawBlock{Count: 1}},
	}}
	results := map[string]*driver.MaterializeResult{
		"bot": {ReadOnly: true, Restart: "on-failure"},
	}
	proxies := []CllamaProxyConfig{{
		ProxyType:      "passthrough",
		Image:          "ghcr.io/mostlydev/cllama-passthrough:latest",
		ContextHostDir: "/tmp/test/.claw-runtime/context",
		AuthHostDir:    "/tmp/test/.claw-runtime/proxy-auth",
		Environment:    map[string]string{"CLAW_POD": "test-pod", "OPENAI_API_KEY": "sk-real"},
		PodName:        "test-pod",
	}}
	out, err := EmitCompose(p, results, proxies)
	if err != nil {
		t.Fatal(err)
	}

	var cf struct {
		Services map[string]interface{} `yaml:"services"`
	}
	yaml.Unmarshal([]byte(out), &cf)
	if _, ok := cf.Services["cllama-passthrough"]; !ok {
		t.Error("expected cllama-passthrough service in output")
	}
	if !strings.Contains(out, "ghcr.io/mostlydev/cllama-passthrough") {
		t.Error("expected proxy image in output")
	}
	if !strings.Contains(out, "claw-internal") {
		t.Error("expected claw-internal network")
	}
}

func TestEmitComposeMultipleProxies(t *testing.T) {
	p := &Pod{Name: "test-pod", Services: map[string]*Service{
		"bot": {Image: "bot:latest", Claw: &ClawBlock{Count: 1}},
	}}
	results := map[string]*driver.MaterializeResult{
		"bot": {ReadOnly: true, Restart: "on-failure"},
	}
	proxies := []CllamaProxyConfig{
		{ProxyType: "passthrough", Image: "ghcr.io/mostlydev/cllama-passthrough:latest", PodName: "test-pod",
			ContextHostDir: "/tmp/ctx", AuthHostDir: "/tmp/auth", Environment: map[string]string{}},
		{ProxyType: "policy", Image: "ghcr.io/mostlydev/cllama-policy:latest", PodName: "test-pod",
			ContextHostDir: "/tmp/ctx", AuthHostDir: "/tmp/auth", Environment: map[string]string{}},
	}
	out, err := EmitCompose(p, results, proxies)
	if err != nil {
		t.Fatal(err)
	}

	var cf struct {
		Services map[string]interface{} `yaml:"services"`
	}
	yaml.Unmarshal([]byte(out), &cf)
	if _, ok := cf.Services["cllama-passthrough"]; !ok {
		t.Error("expected cllama-passthrough service")
	}
	if _, ok := cf.Services["cllama-policy"]; !ok {
		t.Error("expected cllama-policy service")
	}
}

func TestEmitComposeNoProxiesUnchanged(t *testing.T) {
	p := &Pod{Name: "test-pod", Services: map[string]*Service{
		"bot": {Image: "bot:latest", Claw: &ClawBlock{}},
	}}
	results := map[string]*driver.MaterializeResult{
		"bot": {ReadOnly: true, Restart: "on-failure"},
	}
	out, err := EmitCompose(p, results, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "cllama-") {
		t.Error("should not contain proxy when nil")
	}
}
```

**Step 2: Run test — expected FAIL (signature mismatch)**

**Step 3: Implement**

Add `CllamaProxyConfig` type:
```go
type CllamaProxyConfig struct {
	ProxyType      string            // e.g. "passthrough", "policy"
	Image          string            // e.g. "ghcr.io/mostlydev/cllama-passthrough:latest"
	ContextHostDir string            // .claw-runtime/context/
	AuthHostDir    string            // .claw-runtime/proxy-auth/
	Environment    map[string]string // CLAW_POD + provider keys from x-claw.cllama-env
	PodName        string
}
```

Change `EmitCompose` signature: third arg `proxies []CllamaProxyConfig` (nil or empty = no proxies). For each proxy config, inject a compose service named `cllama-<ProxyType>`:
```yaml
cllama-passthrough:
  image: <proxy.Image>
  volumes:
    - <ContextHostDir>:/claw/context:ro
    - <AuthHostDir>:/claw/auth
  environment: <proxy.Environment + CLAW_CONTEXT_ROOT=/claw/context + CLAW_AUTH_DIR=/claw/auth>
  restart: on-failure
  healthcheck:
    test: [CMD, /cllama-passthrough, -healthcheck]
    interval: 15s
    timeout: 5s
    retries: 3
  labels:
    claw.pod: <PodName>
    claw.role: proxy
    claw.proxy.type: <ProxyType>
  networks:
    - claw-internal
```

Update all existing `EmitCompose` call sites to pass `nil`.

**Step 4: Run test — expected PASS. Run `go test ./...` for regression.**

**Step 5: Commit**

`feat(cllama): inject typed cllama proxy services into generated compose`

---

### Task 2.7: Restructure compose_up.go to two-pass loop

**Files:**
- Modify: `cmd/claw/compose_up.go`

This is a refactor with no new behavior. All existing tests must pass.

**Step 1: Run existing tests to establish baseline**

Run: `go test ./...`

**Step 2: Restructure**

Split the current per-service loop (lines 80-258) into:

**Pass 1 (inspect + resolve):** Build `ResolvedClaw`, validate, store driver. Do NOT call `Materialize`. Skill resolution also stays here (it doesn't depend on cllama tokens).

**Pass 2 (materialize):** For each resolved claw, call `d.Materialize(rc, opts)`, assemble skill mounts, store result.

Between the passes: cllama detection, token generation, token assignment, credential starvation, proxy config assembly.

**Step 3: Run tests to verify no regression**

Run: `go test ./...`

**Step 4: Commit**

`refactor(compose_up): two-pass loop to support pre-materialize cllama wiring`

---

### Task 2.8: Wire cllama detection + context + proxy into compose_up.go

**Files:**
- Modify: `cmd/claw/compose_up.go`

Between pass 1 and pass 2, insert:

```go
cllamaEnabled, cllamaAgents := detectCllama(resolvedClaws)
var proxies []pod.CllamaProxyConfig
if cllamaEnabled {
	// Collect the union of proxy types needed across all agents
	proxyTypes := collectProxyTypes(resolvedClaws)

	// Fail-fast: multi-proxy chaining not yet supported (Phase 5 scope)
	if len(proxyTypes) > 1 {
		return fmt.Errorf("multi-proxy chaining not yet supported: found proxy types %v — Phase 4 supports only one proxy type per pod", proxyTypes)
	}

	// Generate bearer tokens — per ordinal when count > 1.
	// For a service "bot" with count=3, generate tokens for bot-0, bot-1, bot-2.
	tokens := make(map[string]string)
	for _, name := range cllamaAgents {
		rc := resolvedClaws[name]
		if rc.Count > 1 {
			for i := 0; i < rc.Count; i++ {
				ordinalName := fmt.Sprintf("%s-%d", name, i)
				tokens[ordinalName] = cllama.GenerateToken(ordinalName)
			}
			// Base service gets first ordinal's token for config generation
			rc.CllamaToken = tokens[fmt.Sprintf("%s-0", name)]
		} else {
			tokens[name] = cllama.GenerateToken(name)
			rc.CllamaToken = tokens[name]
		}
	}

	// Preflight: verify no provider API keys in pod YAML env for cllama agents
	for _, name := range cllamaAgents {
		svc := p.Services[name]
		if svc.Environment != nil {
			for k := range svc.Environment {
				if isProviderKey(k) {
					return fmt.Errorf("service %q: provider key %q found in pod env — cllama requires credential starvation (move to cllama-env block)", name, k)
				}
			}
		}
	}

	// Credential starvation: strip LLM keys from agent environments
	for _, name := range cllamaAgents {
		stripLLMKeys(resolvedClaws[name].Environment)
	}

	// Collect cllama-env from all cllama agents (first one wins on conflict)
	proxyEnv := map[string]string{
		"CLAW_POD": p.Name,
	}
	for _, name := range cllamaAgents {
		svc := p.Services[name]
		if svc.Claw != nil {
			for k, v := range svc.Claw.CllamaEnv {
				if _, exists := proxyEnv[k]; !exists {
					proxyEnv[k] = v
				}
			}
		}
	}

	// Build context inputs — per ordinal when count > 1
	var contextInputs []cllama.AgentContextInput
	for _, name := range cllamaAgents {
		rc := resolvedClaws[name]
		agentContent, err := os.ReadFile(rc.AgentHostPath)
		if err != nil {
			return fmt.Errorf("service %q: read agent for cllama context: %w", name, err)
		}
		md := openclaw.GenerateClawdapusMD(rc, p.Name)
		if rc.Count > 1 {
			for i := 0; i < rc.Count; i++ {
				ordinalName := fmt.Sprintf("%s-%d", name, i)
				contextInputs = append(contextInputs, cllama.AgentContextInput{
					AgentID:     ordinalName,
					AgentsMD:    string(agentContent),
					ClawdapusMD: md,
					Metadata: map[string]interface{}{
						"service": name, "ordinal": i, "pod": p.Name, "type": rc.ClawType,
						"token": tokens[ordinalName],
					},
				})
			}
		} else {
			contextInputs = append(contextInputs, cllama.AgentContextInput{
				AgentID:     name,
				AgentsMD:    string(agentContent),
				ClawdapusMD: md,
				Metadata: map[string]interface{}{
					"service": name, "pod": p.Name, "type": rc.ClawType,
					"token": tokens[name],
				},
			})
		}
	}
	if err := cllama.GenerateContextDir(runtimeDir, contextInputs); err != nil {
		return fmt.Errorf("generate cllama context: %w", err)
	}

	authDir := filepath.Join(runtimeDir, "proxy-auth")
	os.MkdirAll(authDir, 0700)

	// Build one CllamaProxyConfig per proxy type
	// Image convention: ghcr.io/mostlydev/cllama-<type>:latest
	for _, pt := range proxyTypes {
		proxies = append(proxies, pod.CllamaProxyConfig{
			ProxyType:      pt,
			Image:          fmt.Sprintf("ghcr.io/mostlydev/cllama-%s:latest", pt),
			ContextHostDir: filepath.Join(runtimeDir, "context"),
			AuthHostDir:    authDir,
			Environment:    proxyEnv,
			PodName:        p.Name,
		})
	}
	fmt.Printf("[claw] cllama proxies enabled: %s (agents: %s)\n",
		strings.Join(proxyTypes, ", "), strings.Join(cllamaAgents, ", "))
}
```

Also add `stripLLMKeys` and `isProviderKey` helpers:
```go
func isProviderKey(k string) bool {
	switch k {
	case "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "OPENROUTER_API_KEY":
		return true
	}
	return strings.HasPrefix(k, "PROVIDER_API_KEY")
}

func stripLLMKeys(env map[string]string) {
	for k := range env {
		if isProviderKey(k) {
			delete(env, k)
		}
	}
}
```

**Step 1: Write test for stripLLMKeys**

```go
func TestStripLLMKeys(t *testing.T) {
	env := map[string]string{
		"OPENAI_API_KEY": "sk-real", "ANTHROPIC_API_KEY": "sk-ant",
		"DISCORD_BOT_TOKEN": "keep", "LOG_LEVEL": "info",
	}
	stripLLMKeys(env)
	if _, ok := env["OPENAI_API_KEY"]; ok { t.Error("should strip OPENAI_API_KEY") }
	if _, ok := env["ANTHROPIC_API_KEY"]; ok { t.Error("should strip ANTHROPIC_API_KEY") }
	if env["DISCORD_BOT_TOKEN"] != "keep" { t.Error("should keep non-LLM keys") }
}

func TestIsProviderKey(t *testing.T) {
	tests := []struct{ key string; want bool }{
		{"OPENAI_API_KEY", true},
		{"ANTHROPIC_API_KEY", true},
		{"OPENROUTER_API_KEY", true},
		{"PROVIDER_API_KEY_CUSTOM", true},
		{"DISCORD_BOT_TOKEN", false},
		{"LOG_LEVEL", false},
	}
	for _, tt := range tests {
		if got := isProviderKey(tt.key); got != tt.want {
			t.Errorf("isProviderKey(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}
```

**Step 2: Run tests**

**Step 3: Commit**

`feat(cllama): wire detection, tokens, context, and typed proxy configs into compose_up`

---

## SLICE 3: Driver LLM rewiring (credential starvation)

> **Implementation update (2026-02-27):**
> OpenClaw schema rejects `agents.defaults.model.baseURL/apiKey`.
> The shipped implementation rewrites cllama routing at provider scope:
> `models.providers.<provider>.{baseUrl,apiKey,api,models}`.
> Some Task 3.1/3.2 snippets below describe the earlier draft path and are superseded by this provider-level rewrite.

### Task 3.1: OpenClaw driver — rewrite baseURL when cllama active

**Files:**
- Modify: `internal/driver/openclaw/config.go`
- Test: `internal/driver/openclaw/config_test.go`

**Step 1: Write the failing test**

```go
func TestGenerateConfigCllamaRewritesBaseURL(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{"primary": "anthropic/claude-sonnet-4"},
		Cllama: []string{"passthrough"},
	}
	data, err := GenerateConfig(rc)
	if err != nil { t.Fatal(err) }
	var config map[string]interface{}
	json.Unmarshal(data, &config)
	model := config["agents"].(map[string]interface{})["defaults"].(map[string]interface{})["model"].(map[string]interface{})
	// Agent talks to first proxy in chain: cllama-passthrough
	if model["baseURL"] != "http://cllama-passthrough:8080/v1" {
		t.Errorf("expected proxy baseURL, got %v", model["baseURL"])
	}
}

func TestGenerateConfigNoCllamaNoBaseURL(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models: map[string]string{"primary": "anthropic/claude-sonnet-4"},
	}
	data, _ := GenerateConfig(rc)
	var config map[string]interface{}
	json.Unmarshal(data, &config)
	model := config["agents"].(map[string]interface{})["defaults"].(map[string]interface{})["model"].(map[string]interface{})
	if _, exists := model["baseURL"]; exists {
		t.Error("baseURL should not be set when cllama is empty")
	}
}
```

**Step 2: Run test — expected FAIL**

**Step 3: In `GenerateConfig`, after the MODEL loop (line 42), add:**

```go
if len(rc.Cllama) > 0 {
	// Agent talks to the first proxy in its chain: cllama-<first-type>
	firstProxy := fmt.Sprintf("http://cllama-%s:8080/v1", rc.Cllama[0])
	if err := setPath(config, "agents.defaults.model.baseURL", firstProxy); err != nil {
		return nil, fmt.Errorf("config generation: cllama baseURL: %w", err)
	}
}
```

**Step 4: Run test — expected PASS**

**Step 5: Commit**

`feat(cllama): rewrite LLM baseURL to proxy in openclaw config`

---

### Task 3.2: Inject dummy bearer token into openclaw config

**Files:**
- Modify: `internal/driver/openclaw/config.go`
- Test: `internal/driver/openclaw/config_test.go`

**Step 1: Write the failing test**

```go
func TestGenerateConfigCllamaInjectsDummyToken(t *testing.T) {
	rc := &driver.ResolvedClaw{
		Models:      map[string]string{"primary": "anthropic/claude-sonnet-4"},
		Cllama:      []string{"passthrough"},
		CllamaToken: "tiverton:abc123hex",
	}
	data, _ := GenerateConfig(rc)
	var config map[string]interface{}
	json.Unmarshal(data, &config)
	model := config["agents"].(map[string]interface{})["defaults"].(map[string]interface{})["model"].(map[string]interface{})
	if model["apiKey"] != "tiverton:abc123hex" {
		t.Errorf("expected dummy token, got %v", model["apiKey"])
	}
}
```

**Step 2: Run test — expected FAIL**

**Step 3: After the baseURL rewrite, add:**

```go
if len(rc.Cllama) > 0 && rc.CllamaToken != "" {
	if err := setPath(config, "agents.defaults.model.apiKey", rc.CllamaToken); err != nil {
		return nil, fmt.Errorf("config generation: cllama token: %w", err)
	}
}
```

**Step 4: Run test — expected PASS**

**Step 5: Commit**

`feat(cllama): inject dummy bearer token into openclaw config`

---

### Task 3.3: CLAWDAPUS.md — add LLM Proxy section

**Files:**
- Modify: `internal/driver/openclaw/clawdapus_md.go`
- Test: `internal/driver/openclaw/clawdapus_md_test.go`

**Step 1: Write the failing test**

```go
func TestClawdapusMDIncludesProxySection(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "tiverton",
		ClawType:    "openclaw",
		Cllama:      []string{"passthrough"},
	}
	md := GenerateClawdapusMD(rc, "test-pod")
	if !strings.Contains(md, "## LLM Proxy") {
		t.Error("expected LLM Proxy section")
	}
	if !strings.Contains(md, "cllama-passthrough:8080") {
		t.Error("expected proxy endpoint with type name")
	}
}

func TestClawdapusMDMultipleProxies(t *testing.T) {
	rc := &driver.ResolvedClaw{
		ServiceName: "tiverton",
		ClawType:    "openclaw",
		Cllama:      []string{"passthrough", "policy"},
	}
	md := GenerateClawdapusMD(rc, "test-pod")
	if !strings.Contains(md, "passthrough → policy") {
		t.Error("expected chain description")
	}
}

func TestClawdapusMDNoProxyWhenNoCllama(t *testing.T) {
	rc := &driver.ResolvedClaw{ServiceName: "tiverton", ClawType: "openclaw"}
	md := GenerateClawdapusMD(rc, "test-pod")
	if strings.Contains(md, "## LLM Proxy") {
		t.Error("should not include proxy section")
	}
}
```

**Step 2: Run test — expected FAIL**

**Step 3: In `GenerateClawdapusMD`, after the Surfaces section (before Handles), add:**

```go
if len(rc.Cllama) > 0 {
	b.WriteString("## LLM Proxy\n\n")
	b.WriteString("Your LLM requests are routed through a governance proxy.\n\n")
	firstProxy := fmt.Sprintf("cllama-%s", rc.Cllama[0])
	b.WriteString(fmt.Sprintf("- **Endpoint:** `http://%s:8080/v1`\n", firstProxy))
	b.WriteString("- **Auth:** Bearer token (pre-configured)\n")
	if len(rc.Cllama) == 1 {
		b.WriteString(fmt.Sprintf("- **Mode:** %s\n", rc.Cllama[0]))
	} else {
		chain := strings.Join(rc.Cllama, " → ")
		b.WriteString(fmt.Sprintf("- **Chain:** %s\n", chain))
	}
	b.WriteString("\nAll inference requests pass through this proxy for logging and policy enforcement.\n")
	b.WriteString("You do not need to configure this — your model settings are pre-wired.\n\n")
}
```

**Step 4: Run test — expected PASS**

**Step 5: Commit**

`feat(cllama): add LLM Proxy section to CLAWDAPUS.md`

---

### Task 3.4: Documentation fixes (CLLAMA_SPEC.md, CLAUDE.md, ADRs)

**Files:**
- Modify: `docs/CLLAMA_SPEC.md` — fix Section 4.D (`/claw/AGENTS.md` → `/claw/context/<agent-id>/AGENTS.md`), fix Section 6 (`CLAW_ID` → bearer-token identity)
- Modify: `docs/decisions/001-cllama-transport.md` — update to reflect typed proxy naming (`cllama-<type>`) and `Cllama []string`
- Modify: `docs/decisions/008-cllama-sidecar-standard.md` — add note about multi-proxy data model
- Modify: `CLAUDE.md` — update Implementation Status table (Phase 4 → IN PROGRESS), add cllama-related decisions

**Step 1: Fix CLLAMA_SPEC.md**
- Section 4.D: Replace `/claw/AGENTS.md` with `/claw/context/<agent-id>/AGENTS.md`
- Section 6: Replace `CLAW_ID` references with bearer-token identity resolution (`<agent-id>:<secret>`)
- Add note: multi-proxy chaining is spec'd but not yet supported (Phase 5)

**Step 2: Update ADR-001**
- Add note: proxy services are named `cllama-<type>` (e.g. `cllama-passthrough`)
- Add note: `Cllama` field is `[]string`, supporting future chainable proxies

**Step 3: Update CLAUDE.md Implementation Status + Decisions**

**Step 4: Commit**

`docs: fix CLLAMA_SPEC paths, update ADRs for typed proxy naming`

---

## SLICE 4: NOT IN SCOPE (policy pipeline, multi-proxy chain execution — future)

---

## Execution Order

Slices 1 and 2-3 can proceed in parallel (separate repos).

**Slice 1 (cllama-passthrough):** 1.1 → 1.2 → 1.3 → 1.4 → 1.5 → 1.6 → 1.7 → 1.8

**Slices 2+3 (clawdapus):** 2.1 → 2.2 → 2.3 → 2.4 → 2.5 → 2.6 → 2.7 → 2.8 → 3.1 → 3.2 → 3.3 → 3.4

---

## Verification

1. **Unit tests:** `go test ./...` in both repos — all pass
2. **Build proxy:** `docker build -t cllama-passthrough .` in the passthrough repo
3. **Manual E2E (clawdapus):** Create a test `claw-pod.yml` with `x-claw: cllama: passthrough` and `cllama-env: {OPENAI_API_KEY: sk-test}`. Run `claw compose up -d`. Verify:
   - `compose.generated.yml` contains `cllama-passthrough` service (not generic `cllama-proxy`)
   - `.claw-runtime/context/<agent>/` has AGENTS.md, CLAWDAPUS.md, metadata.json
   - Agent's `openclaw.json` has `models.providers.<provider>.baseUrl: http://cllama-passthrough:8080/v1` and dummy provider `apiKey`
   - Agent's environment has NO real API keys
   - Proxy's environment has the real keys from `cllama-env`
4. **Submodule:** Add `cllama-passthrough` as git submodule for E2E tests

---

## Critical Files

**cllama-passthrough (new repo):**
- `cmd/cllama-passthrough/main.go` — dual-server entrypoint
- `internal/identity/identity.go` — bearer token parsing
- `internal/provider/provider.go` — multi-provider registry
- `internal/agentctx/agentctx.go` — context loading
- `internal/proxy/handler.go` — transparent proxy + credential swap
- `internal/logging/logger.go` — structured JSON logging
- `internal/ui/handler.go` — web UI for provider management

**clawdapus (existing repo):**
- `internal/driver/types.go:21-37` — add Cllama + CllamaToken fields
- `cmd/claw/compose_up.go` — two-pass refactor + cllama wiring
- `internal/cllama/token.go` — bearer token generation (new)
- `internal/cllama/context.go` — context dir generation (new)
- `internal/pod/parser.go` — parse x-claw.cllama-env (new field)
- `internal/pod/compose_emit.go` — proxy service injection
- `internal/driver/openclaw/config.go` — provider-level cllama rewrite (`models.providers.*`) + dummy token
- `internal/driver/openclaw/clawdapus_md.go:14-136` — LLM Proxy section

## Decisions to Record (for CLAUDE.md after implementation)

- **`Cllama []string`** — supports multiple proxy types per agent (e.g. `["passthrough", "policy"]`). Clawfile: multiple `CLLAMA` directives. Pod YAML: `cllama: passthrough` (string → single-element list) or `cllama: [passthrough, policy]`
- **Compose service naming**: `cllama-<type>` (e.g. `cllama-passthrough`), not generic `cllama-proxy`. Each proxy type in the union across all agents gets its own compose service
- **Chaining**: Agent provider endpoints (`models.providers.<provider>.baseUrl`) point to the first proxy in `Cllama`. Future proxies chain to next in sequence. Phase 4 only builds passthrough (chain length = 1)
- `x-claw.cllama-env` holds proxy-only env vars (API keys); never injected into agent services
- Auth persisted at `.claw-runtime/proxy-auth/providers.json`; env overrides file; web UI on `:8081` writes to file
- `compose_up.go` uses two-pass loop: pass 1 inspect+resolve, pass 2 materialize (enables pre-materialize token injection)
- `stripLLMKeys` removes OPENAI_API_KEY, ANTHROPIC_API_KEY, OPENROUTER_API_KEY, PROVIDER_API_KEY* from agent env
- Bearer token format: `<service-name>:<48-hex-chars>` via crypto/rand
- Image convention: `ghcr.io/mostlydev/cllama-<type>:latest` (e.g. `ghcr.io/mostlydev/cllama-passthrough:latest`)
