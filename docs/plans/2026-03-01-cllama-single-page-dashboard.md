# cllama Single-Page Dashboard Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace the three-page cllama operator dashboard with a single live-updating page that shows providers (read-only), agent activity, and costs in one vertical scroll.

**Architecture:** One `dashboard.html` template replaces `index.html`, `pod.html`, and `costs.html`. A new SSE endpoint (`GET /events`) pushes a unified JSON state blob every 3 seconds. The client JS listens on the EventSource and patches DOM elements in-place. Provider management (add/edit/delete form) is removed — providers are managed via config file and API only. The `/costs/api` JSON endpoint is preserved for external tooling.

**Tech Stack:** Go (net/http, embed, html/template, SSE via `text/event-stream`), vanilla JS (EventSource)

---

## Task 1: Add SSE Endpoint and Unified State Builder

Add a `GET /events` SSE handler that pushes the full dashboard state as JSON every 3s. This is the foundation all live updates depend on.

**Files:**
- Modify: `cllama/internal/ui/handler.go`
- Test: `cllama/internal/ui/handler_test.go`

**Step 1: Write the failing test for SSE endpoint**

Add to `handler_test.go`:

```go
func TestSSEEndpointStreamsEvents(t *testing.T) {
	reg := provider.NewRegistry(t.TempDir())
	reg.Set("anthropic", &provider.Provider{Name: "anthropic", BaseURL: "https://api.anthropic.com/v1", APIKey: "sk-test", Auth: "bearer"})
	acc := cost.NewAccumulator()
	acc.Record("tiverton", "anthropic", "claude-sonnet-4", 1000, 500, 0.0105)

	h := NewHandler(reg, WithAccumulator(acc))

	req := httptest.NewRequest("GET", "/events", nil)
	w := httptest.NewRecorder()

	// Run handler in goroutine since SSE blocks; cancel via context
	ctx, cancel := context.WithTimeout(req.Context(), 200*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)

	h.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "data:") {
		t.Fatal("expected SSE data line")
	}
	if !strings.Contains(body, "anthropic") {
		t.Error("expected provider name in SSE payload")
	}
	if !strings.Contains(body, "tiverton") {
		t.Error("expected agent name in SSE payload")
	}

	// Verify it's valid JSON inside the data line
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "data:") {
			jsonStr := strings.TrimPrefix(line, "data:")
			var state map[string]interface{}
			if err := json.Unmarshal([]byte(jsonStr), &state); err != nil {
				t.Fatalf("SSE data is not valid JSON: %v", err)
			}
			if _, ok := state["providers"]; !ok {
				t.Error("expected 'providers' key in state")
			}
			if _, ok := state["agents"]; !ok {
				t.Error("expected 'agents' key in state")
			}
			if _, ok := state["totalCostUSD"]; !ok {
				t.Error("expected 'totalCostUSD' key in state")
			}
			break
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd cllama && go test ./internal/ui/ -run TestSSEEndpointStreamsEvents -v`
Expected: FAIL — no route for `/events`

**Step 3: Implement SSE handler and unified state type**

Add to `handler.go`:

```go
// dashboardState is the unified JSON blob pushed via SSE and used for initial render.
type dashboardState struct {
	PodName      string             `json:"podName,omitempty"`
	TotalCostUSD float64            `json:"totalCostUSD"`
	TotalReqs    int                `json:"totalRequests"`
	TotalTokens  int                `json:"totalTokens"`
	Providers    []providerRow      `json:"providers"`
	Agents       []dashboardAgent   `json:"agents"`
}

type dashboardAgent struct {
	AgentID       string            `json:"agentId"`
	Service       string            `json:"service,omitempty"`
	Type          string            `json:"type,omitempty"`
	TotalRequests int               `json:"totalRequests"`
	TotalCostUSD  float64           `json:"totalCostUSD"`
	TotalTokensIn  int             `json:"totalTokensIn"`
	TotalTokensOut int             `json:"totalTokensOut"`
	Models        []dashboardModel  `json:"models"`
}

type dashboardModel struct {
	Provider  string  `json:"provider"`
	Model     string  `json:"model"`
	Requests  int     `json:"requests"`
	TokensIn  int     `json:"tokensIn"`
	TokensOut int     `json:"tokensOut"`
	CostUSD   float64 `json:"costUSD"`
}
```

Add the state builder method:

```go
func (h *Handler) buildDashboardState() dashboardState {
	// Providers
	all := h.registry.All()
	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}
	sort.Strings(names)

	providers := make([]providerRow, 0, len(names))
	for _, name := range names {
		p := all[name]
		providers = append(providers, providerRow{
			Name:      p.Name,
			BaseURL:   p.BaseURL,
			Auth:      p.Auth,
			MaskedKey: maskKey(p.APIKey),
		})
	}

	state := dashboardState{Providers: providers}

	// Pod members + cost data
	if h.contextRoot != "" {
		agents, err := agentctx.ListAgents(h.contextRoot)
		if err == nil {
			for _, a := range agents {
				if state.PodName == "" && a.Pod != "" {
					state.PodName = a.Pod
				}
				da := dashboardAgent{
					AgentID: a.AgentID,
					Service: a.Service,
					Type:    a.Type,
				}
				if h.accumulator != nil {
					entries := h.accumulator.ByAgent(a.AgentID)
					for _, e := range entries {
						da.TotalRequests += e.RequestCount
						da.TotalTokensIn += e.TotalInputTokens
						da.TotalTokensOut += e.TotalOutputTokens
						da.TotalCostUSD += e.TotalCostUSD
						da.Models = append(da.Models, dashboardModel{
							Provider:  e.Provider,
							Model:     e.Model,
							Requests:  e.RequestCount,
							TokensIn:  e.TotalInputTokens,
							TokensOut: e.TotalOutputTokens,
							CostUSD:   e.TotalCostUSD,
						})
					}
				}
				state.Agents = append(state.Agents, da)
			}
		}
	}

	// Agents from cost data that aren't in context (standalone requests)
	if h.accumulator != nil {
		state.TotalCostUSD = h.accumulator.TotalCost()
		grouped := h.accumulator.All()
		knownAgents := make(map[string]bool)
		for _, a := range state.Agents {
			knownAgents[a.AgentID] = true
		}
		unknownIDs := make([]string, 0)
		for id := range grouped {
			if !knownAgents[id] {
				unknownIDs = append(unknownIDs, id)
			}
		}
		sort.Strings(unknownIDs)
		for _, id := range unknownIDs {
			entries := grouped[id]
			da := dashboardAgent{AgentID: id}
			for _, e := range entries {
				da.TotalRequests += e.RequestCount
				da.TotalTokensIn += e.TotalInputTokens
				da.TotalTokensOut += e.TotalOutputTokens
				da.TotalCostUSD += e.TotalCostUSD
				da.Models = append(da.Models, dashboardModel{
					Provider:  e.Provider,
					Model:     e.Model,
					Requests:  e.RequestCount,
					TokensIn:  e.TotalInputTokens,
					TokensOut: e.TotalOutputTokens,
					CostUSD:   e.TotalCostUSD,
				})
			}
			state.Agents = append(state.Agents, da)
		}
		for _, a := range state.Agents {
			state.TotalReqs += a.TotalRequests
			state.TotalTokens += a.TotalTokensIn + a.TotalTokensOut
		}
	}

	sort.Slice(state.Agents, func(i, j int) bool {
		return state.Agents[i].AgentID < state.Agents[j].AgentID
	})

	return state
}
```

Add the SSE handler:

```go
func (h *Handler) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	// Send initial state immediately
	h.writeSSEEvent(w, flusher)

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			h.writeSSEEvent(w, flusher)
		}
	}
}

func (h *Handler) writeSSEEvent(w http.ResponseWriter, flusher http.Flusher) {
	state := h.buildDashboardState()
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data:%s\n\n", data)
	flusher.Flush()
}
```

Update the route table in `ServeHTTP`:

```go
case r.Method == http.MethodGet && r.URL.Path == "/events":
	h.handleSSE(w, r)
	return
```

**Step 4: Run test to verify it passes**

Run: `cd cllama && go test ./internal/ui/ -run TestSSEEndpointStreamsEvents -v`
Expected: PASS

**Step 5: Commit**

```bash
git add cllama/internal/ui/handler.go cllama/internal/ui/handler_test.go
git commit -m "feat(cllama): add SSE endpoint and unified dashboard state builder"
```

---

## Task 2: Create Single-Page Dashboard Template

Replace three templates with one `dashboard.html`. Vertical stack: header, providers strip, agent cards with costs inline. Vanilla JS EventSource for live updates.

**Files:**
- Create: `cllama/internal/ui/templates/dashboard.html`
- Modify: `cllama/internal/ui/handler.go` (route `/` to dashboard)
- Test: `cllama/internal/ui/handler_test.go`

**Step 1: Write the failing test**

Add to `handler_test.go`:

```go
func TestDashboardRendersAllSections(t *testing.T) {
	reg := provider.NewRegistry(t.TempDir())
	reg.Set("anthropic", &provider.Provider{Name: "anthropic", BaseURL: "https://api.anthropic.com/v1", APIKey: "sk-test-key-1234", Auth: "bearer"})
	acc := cost.NewAccumulator()
	acc.Record("tiverton", "anthropic", "claude-sonnet-4", 1000, 500, 0.0105)

	h := NewHandler(reg, WithAccumulator(acc))
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()

	// Should contain provider info (read-only)
	if !strings.Contains(body, "anthropic") {
		t.Error("expected provider name in dashboard")
	}
	if !strings.Contains(body, "sk-t...1234") {
		t.Error("expected masked API key in dashboard")
	}
	// Should NOT contain provider form
	if strings.Contains(body, "method=\"post\"") {
		t.Error("dashboard should not contain provider management form")
	}
	// Should contain SSE connection script
	if !strings.Contains(body, "EventSource") {
		t.Error("expected EventSource script for live updates")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd cllama && go test ./internal/ui/ -run TestDashboardRendersAllSections -v`
Expected: FAIL — still rendering old `index.html`

**Step 3: Create `dashboard.html`**

Create `cllama/internal/ui/templates/dashboard.html`. This is a single template with:

- **Header bar:** cllama branding, pod name, total spend, live indicator
- **Providers section:** read-only compact table (name, URL, auth, masked key)
- **Agent cards:** responsive grid with agent ID, type badge, requests, cost, token breakdown, model tags
- **JS:** EventSource on `/events`, updates all dynamic elements by `data-*` attributes
- **Single color scheme:** cyan accent throughout, amber for cost values

The template receives `dashboardState` for initial server-render. JS patches values live.

Key design rules for the template:
- One `<style>` block, ~400 lines, no duplication
- CSS variables from existing design system (keep `--bg`, `--bg-raised`, `--cyan`, `--amber`, etc.)
- Same fonts (Geist Mono + Outfit)
- Responsive: single column below 720px
- `data-total-cost`, `data-total-requests`, `data-total-tokens` on header stats
- `data-agent="<id>"` on each agent card for targeted updates
- `data-field="requests"`, `data-field="cost"`, etc. on updatable values

The `<script>` block (~40 lines):
```javascript
const es = new EventSource('/events');
es.onmessage = function(e) {
  const state = JSON.parse(e.data);
  // Update header stats
  const el = (sel) => document.querySelector(sel);
  el('[data-total-cost]').textContent = '$' + state.totalCostUSD.toFixed(4);
  el('[data-total-requests]').textContent = state.totalRequests;
  el('[data-total-tokens]').textContent = state.totalTokens;
  // Update agent cards
  state.agents.forEach(a => {
    const card = document.querySelector(`[data-agent="${a.agentId}"]`);
    if (!card) return; // new agent — needs page reload
    card.querySelector('[data-field="requests"]').textContent = a.totalRequests;
    card.querySelector('[data-field="cost"]').textContent = '$' + a.totalCostUSD.toFixed(4);
    card.querySelector('[data-field="tokens-in"]').textContent = a.totalTokensIn;
    card.querySelector('[data-field="tokens-out"]').textContent = a.totalTokensOut;
  });
};
es.onerror = function() {
  document.querySelector('.live-indicator').classList.add('disconnected');
};
```

**Step 4: Update handler to route `/` to dashboard**

In `handler.go`, change `renderIndex` to `renderDashboard`:

```go
func (h *Handler) renderDashboard(w http.ResponseWriter) {
	state := h.buildDashboardState()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = h.tpl.ExecuteTemplate(w, "dashboard.html", state)
}
```

Update the route in `ServeHTTP`:

```go
case r.Method == http.MethodGet && r.URL.Path == "/":
	h.renderDashboard(w)
	return
```

**Step 5: Run test to verify it passes**

Run: `cd cllama && go test ./internal/ui/ -run TestDashboardRendersAllSections -v`
Expected: PASS

**Step 6: Commit**

```bash
git add cllama/internal/ui/templates/dashboard.html cllama/internal/ui/handler.go cllama/internal/ui/handler_test.go
git commit -m "feat(cllama): single-page dashboard with SSE live updates"
```

---

## Task 3: Remove Old Templates and Dead Code

Delete the three old templates, remove the provider form handler, remove old page data types and render methods. Clean up routes.

**Files:**
- Delete: `cllama/internal/ui/templates/index.html`
- Delete: `cllama/internal/ui/templates/pod.html`
- Delete: `cllama/internal/ui/templates/costs.html`
- Modify: `cllama/internal/ui/handler.go`
- Modify: `cllama/internal/ui/handler_test.go`

**Step 1: Delete old templates**

```bash
rm cllama/internal/ui/templates/index.html
rm cllama/internal/ui/templates/pod.html
rm cllama/internal/ui/templates/costs.html
```

**Step 2: Remove dead code from handler.go**

Remove from `handler.go`:
- `type pageData struct` (replaced by `dashboardState`)
- `type costsPageData struct` and its sub-types `agentCostRow`, `modelCostRow`
- `type podPageData struct` and `podMemberRow`
- `func (h *Handler) renderIndex(...)`
- `func (h *Handler) handleProviderUpdate(...)`
- `func (h *Handler) renderCosts(...)`
- `func (h *Handler) buildCostsPageData()`
- `func (h *Handler) renderPod(...)`
- `func (h *Handler) buildPodPageData()`

Remove from `ServeHTTP` route table:
- `POST /providers` route
- `GET /pod` route
- `GET /costs` route

Keep:
- `GET /` → `renderDashboard`
- `GET /events` → `handleSSE`
- `GET /costs/api` → `handleCostsAPI` (external tooling)
- `maskKey` helper
- Cost API types (`costsAPIResponse`, `agentAPIResponse`, `modelAPIResponse`)
- `buildCostsAPIResponse` (used by `/costs/api`)

Final `ServeHTTP`:

```go
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/":
		h.renderDashboard(w)
	case r.Method == http.MethodGet && r.URL.Path == "/events":
		h.handleSSE(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/costs/api":
		h.handleCostsAPI(w)
	default:
		http.NotFound(w, r)
	}
}
```

**Step 3: Update tests**

Remove tests for deleted functionality:
- `TestUIUpsertProvider` — form handler removed
- `TestUIDeleteProvider` — form handler removed
- `TestUICostsPageRenders` — `/costs` page removed
- `TestUICostsPageRendersEmpty` — `/costs` page removed

Update `TestUIListsProviders` to test the new dashboard route instead (rename to `TestDashboardListsProviders` — but this is now covered by `TestDashboardRendersAllSections` from Task 2, so delete it).

Keep:
- `TestMaskKey` — utility still used
- `TestNotFound` — still valid
- `TestUICostsAPIReturnsJSON` — endpoint preserved
- `TestUICostsAPIEmptyAccumulator` — endpoint preserved
- `TestSSEEndpointStreamsEvents` — new from Task 1
- `TestDashboardRendersAllSections` — new from Task 2

**Step 4: Run all tests**

Run: `cd cllama && go test ./internal/ui/ -v`
Expected: All pass, no references to deleted templates

**Step 5: Run full test suite**

Run: `cd cllama && go test ./...`
Expected: All pass

**Step 6: Commit**

```bash
git add -A cllama/internal/ui/
git commit -m "refactor(cllama): remove old multi-page templates and provider form handler"
```

---

## Task 4: Verify End-to-End and Final Cleanup

Verify the complete build, run a quick manual sanity check if possible, ensure no dead imports or unused code.

**Files:**
- Modify: `cllama/internal/ui/handler.go` (if cleanup needed)

**Step 1: Run vet**

Run: `cd cllama && go vet ./...`
Expected: Clean

**Step 2: Build binary**

Run: `cd cllama && go build -o /dev/null ./cmd/cllama`
Expected: Success

**Step 3: Run full test suite one more time**

Run: `cd cllama && go test ./...`
Expected: All pass

**Step 4: Commit any cleanup**

Only if Steps 1-3 revealed issues. Otherwise skip.

**Step 5: Final commit (if needed)**

```bash
git add cllama/
git commit -m "chore(cllama): final cleanup for single-page dashboard"
```
