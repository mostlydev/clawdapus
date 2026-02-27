# cllama Cost Hooks — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add per-agent, per-model cost tracking to cllama-passthrough so operators see real-time spend per agent, with cost data in structured logs and a dashboard on the existing :8081 web UI.

**Architecture:** The proxy already intercepts every LLM request and response. Cost hooks read the `usage` block from OpenAI-compatible responses, multiply by a pricing table, and aggregate in-memory per agent. A new `internal/cost/` package owns the pricing table and accumulator. The logger gains `tokens_in`, `tokens_out`, `cost_usd` fields. The UI gains a `/costs` page. No persistent storage — costs reset on proxy restart (persistence is a future concern; structured logs are the durable record).

**Tech Stack:** Go 1.23, `sync` (thread-safe accumulator), `encoding/json` (response parsing), `html/template` (UI), `time` (windowed stats)

**Repo:** `mostlydev/cllama-passthrough` (at `/Users/wojtek/dev/ai/clawdapus/cllama-passthrough`)

---

## Key Design Decisions

| Decision | Resolution |
|----------|-----------|
| Where does cost extraction happen? | In `proxy/handler.go` after streaming the response. Tee the response body to capture usage without breaking SSE streaming. |
| Pricing source | Embedded Go map, updated manually. Good enough for passthrough. Future: fetch from provider APIs. |
| Accumulator scope | In-memory, per-process. Resets on restart. Structured logs are the durable record. |
| Aggregation keys | `(agent_id, provider, model)` — enables drill-down by agent, by provider, or by model. |
| SSE handling | For streamed responses, usage block appears in the final `data: [DONE]`-preceding chunk. Buffer the last SSE event to extract usage. For non-streamed, parse response body JSON directly. |
| Budget caps | OUT OF SCOPE. This plan is observation only. Caps/alerts are a future slice. |
| Cost precision | `float64` USD. Good enough — we're tracking cents, not derivatives. |

---

## SLICE 1: Cost accumulator + pricing table

### Task 1.1: Pricing table with known models

**Files:**
- Create: `internal/cost/pricing.go`
- Create: `internal/cost/pricing_test.go`

**Step 1: Write the failing test**

```go
package cost

import "testing"

func TestLookupKnownModel(t *testing.T) {
	p := DefaultPricing()
	rate, ok := p.Lookup("anthropic", "claude-sonnet-4")
	if !ok {
		t.Fatal("expected to find claude-sonnet-4")
	}
	if rate.InputPerMTok <= 0 || rate.OutputPerMTok <= 0 {
		t.Errorf("expected positive rates, got in=%f out=%f", rate.InputPerMTok, rate.OutputPerMTok)
	}
}

func TestLookupUnknownModelReturnsFalse(t *testing.T) {
	p := DefaultPricing()
	_, ok := p.Lookup("anthropic", "nonexistent-model")
	if ok {
		t.Error("expected false for unknown model")
	}
}

func TestLookupOpenAIModel(t *testing.T) {
	p := DefaultPricing()
	rate, ok := p.Lookup("openai", "gpt-4o")
	if !ok {
		t.Fatal("expected to find gpt-4o")
	}
	if rate.InputPerMTok <= 0 {
		t.Error("expected positive input rate")
	}
}

func TestComputeCost(t *testing.T) {
	rate := Rate{InputPerMTok: 3.0, OutputPerMTok: 15.0}
	cost := rate.Compute(1000, 500)
	// 1000 input tokens = 1000/1_000_000 * 3.0 = 0.003
	// 500 output tokens = 500/1_000_000 * 15.0 = 0.0075
	expected := 0.003 + 0.0075
	if cost < expected-0.0001 || cost > expected+0.0001 {
		t.Errorf("expected ~%f, got %f", expected, cost)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/cost/ -v`
Expected: FAIL — package not found

**Step 3: Write implementation**

```go
package cost

// Rate is the per-million-token price in USD.
type Rate struct {
	InputPerMTok  float64
	OutputPerMTok float64
}

// Compute returns cost in USD for the given token counts.
func (r Rate) Compute(inputTokens, outputTokens int) float64 {
	return float64(inputTokens)/1_000_000*r.InputPerMTok +
		float64(outputTokens)/1_000_000*r.OutputPerMTok
}

// Pricing is a lookup table: provider → model → rate.
type Pricing struct {
	rates map[string]map[string]Rate
}

// Lookup returns the rate for a provider/model pair.
func (p *Pricing) Lookup(provider, model string) (Rate, bool) {
	models, ok := p.rates[provider]
	if !ok {
		return Rate{}, false
	}
	rate, ok := models[model]
	return rate, ok
}

// DefaultPricing returns a pricing table with well-known models.
// Prices in USD per million tokens. Updated manually.
func DefaultPricing() *Pricing {
	return &Pricing{rates: map[string]map[string]Rate{
		"anthropic": {
			"claude-sonnet-4":   {InputPerMTok: 3.0, OutputPerMTok: 15.0},
			"claude-sonnet-4-6": {InputPerMTok: 3.0, OutputPerMTok: 15.0},
			"claude-haiku-4-5":  {InputPerMTok: 0.80, OutputPerMTok: 4.0},
			"claude-opus-4":     {InputPerMTok: 15.0, OutputPerMTok: 75.0},
			"claude-opus-4-6":   {InputPerMTok: 15.0, OutputPerMTok: 75.0},
		},
		"openai": {
			"gpt-4o":      {InputPerMTok: 2.50, OutputPerMTok: 10.0},
			"gpt-4o-mini": {InputPerMTok: 0.15, OutputPerMTok: 0.60},
			"gpt-4.1":     {InputPerMTok: 2.0, OutputPerMTok: 8.0},
			"gpt-4.1-mini": {InputPerMTok: 0.40, OutputPerMTok: 1.60},
			"gpt-4.1-nano": {InputPerMTok: 0.10, OutputPerMTok: 0.40},
			"o3":          {InputPerMTok: 2.0, OutputPerMTok: 8.0},
			"o4-mini":     {InputPerMTok: 1.10, OutputPerMTok: 4.40},
		},
		"openrouter": {
			// OpenRouter charges vary per model; use rough estimates.
			// Operators can override via pricing.json in the future.
		},
	}}
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/cost/ -v`

**Step 5: Commit**

`feat(cost): pricing table with known model rates`

---

### Task 1.2: In-memory cost accumulator

**Files:**
- Create: `internal/cost/accumulator.go`
- Create: `internal/cost/accumulator_test.go`

**Step 1: Write the failing test**

```go
package cost

import "testing"

func TestAccumulatorRecordAndQuery(t *testing.T) {
	a := NewAccumulator()
	a.Record("tiverton", "anthropic", "claude-sonnet-4", 1000, 500, 0.0105)
	a.Record("tiverton", "anthropic", "claude-sonnet-4", 2000, 1000, 0.021)

	summary := a.ByAgent("tiverton")
	if len(summary) != 1 {
		t.Fatalf("expected 1 model entry, got %d", len(summary))
	}
	entry := summary[0]
	if entry.TotalInputTokens != 3000 {
		t.Errorf("expected 3000 input tokens, got %d", entry.TotalInputTokens)
	}
	if entry.TotalOutputTokens != 1500 {
		t.Errorf("expected 1500 output tokens, got %d", entry.TotalOutputTokens)
	}
	if entry.TotalCostUSD < 0.031 || entry.TotalCostUSD > 0.032 {
		t.Errorf("expected ~0.0315 cost, got %f", entry.TotalCostUSD)
	}
	if entry.RequestCount != 2 {
		t.Errorf("expected 2 requests, got %d", entry.RequestCount)
	}
}

func TestAccumulatorAllAgents(t *testing.T) {
	a := NewAccumulator()
	a.Record("tiverton", "anthropic", "claude-sonnet-4", 100, 50, 0.001)
	a.Record("westin", "openai", "gpt-4o", 200, 100, 0.002)

	all := a.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(all))
	}
}

func TestAccumulatorTotalCost(t *testing.T) {
	a := NewAccumulator()
	a.Record("tiverton", "anthropic", "claude-sonnet-4", 100, 50, 0.001)
	a.Record("westin", "openai", "gpt-4o", 200, 100, 0.002)
	total := a.TotalCost()
	if total < 0.002 || total > 0.004 {
		t.Errorf("expected ~0.003, got %f", total)
	}
}
```

**Step 2: Run test — expected FAIL**

**Step 3: Write implementation**

```go
package cost

import (
	"sort"
	"sync"
)

// CostEntry is one (agent, provider, model) cost bucket.
type CostEntry struct {
	AgentID           string
	Provider          string
	Model             string
	TotalInputTokens  int
	TotalOutputTokens int
	TotalCostUSD      float64
	RequestCount      int
}

type bucketKey struct {
	AgentID  string
	Provider string
	Model    string
}

// Accumulator aggregates per-request cost data in memory. Thread-safe.
type Accumulator struct {
	mu      sync.RWMutex
	buckets map[bucketKey]*CostEntry
}

func NewAccumulator() *Accumulator {
	return &Accumulator{buckets: make(map[bucketKey]*CostEntry)}
}

func (a *Accumulator) Record(agentID, provider, model string, inputTokens, outputTokens int, costUSD float64) {
	key := bucketKey{AgentID: agentID, Provider: provider, Model: model}
	a.mu.Lock()
	defer a.mu.Unlock()
	e, ok := a.buckets[key]
	if !ok {
		e = &CostEntry{AgentID: agentID, Provider: provider, Model: model}
		a.buckets[key] = e
	}
	e.TotalInputTokens += inputTokens
	e.TotalOutputTokens += outputTokens
	e.TotalCostUSD += costUSD
	e.RequestCount++
}

// ByAgent returns all cost entries for a given agent, sorted by model.
func (a *Accumulator) ByAgent(agentID string) []CostEntry {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var out []CostEntry
	for _, e := range a.buckets {
		if e.AgentID == agentID {
			out = append(out, *e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Provider+"/"+out[i].Model < out[j].Provider+"/"+out[j].Model
	})
	return out
}

// All returns cost summaries grouped by agent, sorted by agent ID.
func (a *Accumulator) All() map[string][]CostEntry {
	a.mu.RLock()
	defer a.mu.RUnlock()
	grouped := make(map[string][]CostEntry)
	for _, e := range a.buckets {
		grouped[e.AgentID] = append(grouped[e.AgentID], *e)
	}
	for k := range grouped {
		sort.Slice(grouped[k], func(i, j int) bool {
			return grouped[k][i].Provider+"/"+grouped[k][i].Model < grouped[k][j].Provider+"/"+grouped[k][j].Model
		})
	}
	return grouped
}

// TotalCost returns the sum of all recorded costs across all agents.
func (a *Accumulator) TotalCost() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var total float64
	for _, e := range a.buckets {
		total += e.TotalCostUSD
	}
	return total
}
```

**Step 4: Run test — expected PASS**

Run: `go test ./internal/cost/ -v`

**Step 5: Commit**

`feat(cost): in-memory per-agent cost accumulator`

---

## SLICE 2: Response usage extraction + handler integration

### Task 2.1: Extract usage from OpenAI-compatible response body

**Files:**
- Create: `internal/cost/usage.go`
- Create: `internal/cost/usage_test.go`

**Step 1: Write the failing test**

```go
package cost

import "testing"

func TestExtractUsageFromJSON(t *testing.T) {
	body := []byte(`{
		"id": "chatcmpl-1",
		"choices": [{"message": {"content": "hello"}}],
		"usage": {
			"prompt_tokens": 150,
			"completion_tokens": 42,
			"total_tokens": 192
		}
	}`)

	u, err := ExtractUsage(body)
	if err != nil {
		t.Fatal(err)
	}
	if u.PromptTokens != 150 {
		t.Errorf("expected 150 prompt tokens, got %d", u.PromptTokens)
	}
	if u.CompletionTokens != 42 {
		t.Errorf("expected 42 completion tokens, got %d", u.CompletionTokens)
	}
}

func TestExtractUsageMissing(t *testing.T) {
	body := []byte(`{"id": "chatcmpl-1", "choices": []}`)
	u, err := ExtractUsage(body)
	if err != nil {
		t.Fatal(err)
	}
	if u.PromptTokens != 0 || u.CompletionTokens != 0 {
		t.Errorf("expected zero usage when missing, got %+v", u)
	}
}

func TestExtractUsageFromSSE(t *testing.T) {
	// SSE stream: final data chunk before [DONE] contains usage
	stream := []byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":20,\"total_tokens\":120}}\n\n" +
		"data: [DONE]\n\n")
	u, err := ExtractUsageFromSSE(stream)
	if err != nil {
		t.Fatal(err)
	}
	if u.PromptTokens != 100 {
		t.Errorf("expected 100 prompt tokens, got %d", u.PromptTokens)
	}
	if u.CompletionTokens != 20 {
		t.Errorf("expected 20 completion tokens, got %d", u.CompletionTokens)
	}
}

func TestExtractUsageFromSSENoUsage(t *testing.T) {
	stream := []byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n")
	u, err := ExtractUsageFromSSE(stream)
	if err != nil {
		t.Fatal(err)
	}
	if u.PromptTokens != 0 {
		t.Errorf("expected 0, got %d", u.PromptTokens)
	}
}
```

**Step 2: Run test — expected FAIL**

**Step 3: Write implementation**

```go
package cost

import (
	"bytes"
	"encoding/json"
)

// Usage holds token counts from an OpenAI-compatible response.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ExtractUsage parses usage from a non-streamed JSON response body.
func ExtractUsage(body []byte) (Usage, error) {
	var resp struct {
		Usage *Usage `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return Usage{}, err
	}
	if resp.Usage == nil {
		return Usage{}, nil
	}
	return *resp.Usage, nil
}

// ExtractUsageFromSSE scans SSE data lines for the last one containing a "usage" field.
// OpenAI streams include usage in the final data chunk before "data: [DONE]".
func ExtractUsageFromSSE(stream []byte) (Usage, error) {
	var lastUsage Usage
	for _, line := range bytes.Split(stream, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		payload := bytes.TrimPrefix(line, []byte("data: "))
		if bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		var chunk struct {
			Usage *Usage `json:"usage"`
		}
		if json.Unmarshal(payload, &chunk) == nil && chunk.Usage != nil {
			lastUsage = *chunk.Usage
		}
	}
	return lastUsage, nil
}
```

**Step 4: Run test — expected PASS**

**Step 5: Commit**

`feat(cost): extract usage from JSON and SSE response bodies`

---

### Task 2.2: Tee response body in proxy handler + record cost

**Files:**
- Modify: `internal/proxy/handler.go`
- Modify: `internal/proxy/handler_test.go`

This is the critical integration point. The handler currently calls `streamBody(w, resp.Body)` which consumes the response. We need to tee it so we can also extract usage.

**Step 1: Write the failing test**

Add to `handler_test.go`:

```go
func TestHandlerRecordsCost(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"id": "chatcmpl-1",
			"choices": [{"message": {"content": "hello"}}],
			"usage": {"prompt_tokens": 100, "completion_tokens": 50, "total_tokens": 150}
		}`))
	}))
	defer backend.Close()

	reg := provider.NewRegistry("")
	reg.Set("anthropic", &provider.Provider{
		Name: "anthropic", BaseURL: backend.URL, APIKey: "sk-real", Auth: "bearer",
	})

	acc := cost.NewAccumulator()
	pricing := cost.DefaultPricing()
	h := NewHandler(reg, stubContextLoader("tiverton"), logging.New(io.Discard),
		WithCostTracking(acc, pricing))

	body := `{"model":"anthropic/claude-sonnet-4","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer tiverton:dummy123")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	entries := acc.ByAgent("tiverton")
	if len(entries) == 0 {
		t.Fatal("expected cost entry recorded")
	}
	if entries[0].TotalInputTokens != 100 {
		t.Errorf("expected 100 input tokens, got %d", entries[0].TotalInputTokens)
	}
	if entries[0].TotalCostUSD <= 0 {
		t.Error("expected positive cost")
	}
}
```

**Step 2: Run test — expected FAIL (WithCostTracking not defined)**

**Step 3: Modify handler.go**

Add optional cost tracking fields to Handler via functional options:

```go
// Add to Handler struct:
	accumulator *cost.Accumulator
	pricing     *cost.Pricing

// Add option type and constructor:
type HandlerOption func(*Handler)

func WithCostTracking(acc *cost.Accumulator, pricing *cost.Pricing) HandlerOption {
	return func(h *Handler) {
		h.accumulator = acc
		h.pricing = pricing
	}
}
```

Update `NewHandler` to accept `...HandlerOption` and apply them.

After `streamBody` succeeds and before `LogResponse`, add cost extraction:

```go
// After streaming completes, extract usage from captured response
if h.accumulator != nil && h.pricing != nil {
	captured := responseBuf.Bytes()
	var usage cost.Usage
	if isSSE(resp.Header) {
		usage, _ = cost.ExtractUsageFromSSE(captured)
	} else {
		usage, _ = cost.ExtractUsage(captured)
	}
	if usage.PromptTokens > 0 || usage.CompletionTokens > 0 {
		rate, ok := h.pricing.Lookup(providerName, upstreamModel)
		costUSD := 0.0
		if ok {
			costUSD = rate.Compute(usage.PromptTokens, usage.CompletionTokens)
		}
		h.accumulator.Record(agentID, providerName, upstreamModel,
			usage.PromptTokens, usage.CompletionTokens, costUSD)
	}
}
```

Replace `streamBody(w, resp.Body)` with a tee pattern:

```go
var responseBuf bytes.Buffer
tee := io.TeeReader(resp.Body, &responseBuf)
if err := streamBody(w, tee); err != nil {
	// ...
}
```

**Step 4: Run test — expected PASS**

Run: `go test ./internal/proxy/ -v`

**Step 5: Commit**

`feat(cost): tee response body and record per-agent costs in proxy handler`

---

### Task 2.3: Add cost fields to structured logger

**Files:**
- Modify: `internal/logging/logger.go`
- Modify: `internal/logging/logger_test.go`

**Step 1: Write the failing test**

Add to `logger_test.go`:

```go
func TestLogResponseIncludesCostFields(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf)
	l.LogResponseWithCost("tiverton", "anthropic/claude-sonnet-4", 200, 1250,
		&CostInfo{InputTokens: 100, OutputTokens: 50, CostUSD: 0.0105})

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if entry["tokens_in"].(float64) != 100 {
		t.Errorf("expected tokens_in=100, got %v", entry["tokens_in"])
	}
	if entry["tokens_out"].(float64) != 50 {
		t.Errorf("expected tokens_out=50, got %v", entry["tokens_out"])
	}
	if entry["cost_usd"].(float64) < 0.01 || entry["cost_usd"].(float64) > 0.02 {
		t.Errorf("expected cost_usd ~0.0105, got %v", entry["cost_usd"])
	}
}
```

**Step 2: Run test — expected FAIL**

**Step 3: Add `CostInfo` type and `LogResponseWithCost` method**

```go
type CostInfo struct {
	InputTokens  int
	OutputTokens int
	CostUSD      float64
}
```

Add optional cost fields to the `entry` struct: `TokensIn *int`, `TokensOut *int`, `CostUSD *float64`. Add `LogResponseWithCost` that populates them.

Update `handler.go` to call `LogResponseWithCost` when cost data is available, falling back to `LogResponse` when not.

**Step 4: Run test — expected PASS**

**Step 5: Commit**

`feat(cost): add tokens_in, tokens_out, cost_usd to structured logs`

---

## SLICE 3: Cost dashboard in web UI

### Task 3.1: Add `/costs` endpoint to UI handler

**Files:**
- Modify: `internal/ui/handler.go`
- Create: `internal/ui/templates/costs.html`
- Modify: `internal/ui/handler_test.go`

**Step 1: Write the failing test**

```go
func TestUICostsPageRenders(t *testing.T) {
	reg := provider.NewRegistry("")
	acc := cost.NewAccumulator()
	acc.Record("tiverton", "anthropic", "claude-sonnet-4", 1000, 500, 0.0105)
	acc.Record("westin", "openai", "gpt-4o", 2000, 1000, 0.035)

	h := NewHandler(reg, WithAccumulator(acc))
	req := httptest.NewRequest("GET", "/costs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "tiverton") {
		t.Error("expected agent name in response")
	}
	if !strings.Contains(body, "0.01") {
		t.Error("expected cost value in response")
	}
}
```

**Step 2: Run test — expected FAIL**

**Step 3: Implement**

Add `WithAccumulator` option to `ui.NewHandler`. Add route for `GET /costs`. Create `costs.html` template showing:

- Total spend across all agents
- Per-agent table: agent name, total requests, total tokens in/out, total cost
- Per-agent breakdown by model when you click/expand

The template uses the same CSS as `index.html`. Add a nav link between providers page and costs page.

**Step 4: Run test — expected PASS**

**Step 5: Commit**

`feat(cost): add /costs dashboard to web UI`

---

### Task 3.2: Add `/costs/api` JSON endpoint for programmatic access

**Files:**
- Modify: `internal/ui/handler.go`
- Modify: `internal/ui/handler_test.go`

**Step 1: Write the failing test**

```go
func TestUICostsAPIReturnsJSON(t *testing.T) {
	reg := provider.NewRegistry("")
	acc := cost.NewAccumulator()
	acc.Record("tiverton", "anthropic", "claude-sonnet-4", 1000, 500, 0.0105)

	h := NewHandler(reg, WithAccumulator(acc))
	req := httptest.NewRequest("GET", "/costs/api", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected JSON content type, got %q", ct)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := result["total_cost_usd"]; !ok {
		t.Error("expected total_cost_usd field")
	}
	if _, ok := result["agents"]; !ok {
		t.Error("expected agents field")
	}
}
```

**Step 2: Run test — expected FAIL**

**Step 3: Add `GET /costs/api` route** that returns:

```json
{
  "total_cost_usd": 0.0105,
  "agents": {
    "tiverton": {
      "total_cost_usd": 0.0105,
      "total_requests": 1,
      "models": [
        {
          "provider": "anthropic",
          "model": "claude-sonnet-4",
          "input_tokens": 1000,
          "output_tokens": 500,
          "cost_usd": 0.0105,
          "requests": 1
        }
      ]
    }
  }
}
```

**Step 4: Run test — expected PASS**

**Step 5: Commit**

`feat(cost): add /costs/api JSON endpoint for programmatic access`

---

### Task 3.3: Wire cost tracking into main.go

**Files:**
- Modify: `cmd/cllama-passthrough/main.go`
- Modify: `cmd/cllama-passthrough/main_test.go`

**Step 1: Wire accumulator + pricing into both servers**

In `run()`, create:

```go
pricing := cost.DefaultPricing()
acc := cost.NewAccumulator()
```

Pass to proxy handler:

```go
proxy.NewHandler(reg, contextLoader, logger, proxy.WithCostTracking(acc, pricing))
```

Pass to UI handler:

```go
ui.NewHandler(reg, ui.WithAccumulator(acc))
```

**Step 2: Update integration test** to verify a round-trip populates `/costs/api`:

```go
func TestIntegrationCostTracking(t *testing.T) {
	// Start both servers on random ports, send a proxied request,
	// then hit /costs/api and verify the cost was recorded.
}
```

**Step 3: Run tests**

Run: `go test ./... -v`

**Step 4: Commit**

`feat(cost): wire cost tracking into main entrypoint`

---

## Execution Order

1.1 → 1.2 → 2.1 → 2.2 → 2.3 → 3.1 → 3.2 → 3.3

Linear — each task builds on the previous.

---

## Verification

1. `go test ./...` — all pass
2. `go build ./cmd/cllama-passthrough` — compiles
3. Manual: start proxy with a mock backend, send a request, check:
   - Structured log line includes `tokens_in`, `tokens_out`, `cost_usd`
   - `/costs` page shows the agent and cost
   - `/costs/api` returns JSON with the recorded cost

---

## Critical Files

- `internal/cost/pricing.go` — pricing table (new)
- `internal/cost/accumulator.go` — per-agent cost aggregator (new)
- `internal/cost/usage.go` — response body usage extraction (new)
- `internal/proxy/handler.go:51-158` — tee + cost recording integration
- `internal/logging/logger.go:46-56` — cost fields on response logs
- `internal/ui/handler.go` — `/costs` + `/costs/api` routes
- `internal/ui/templates/costs.html` — cost dashboard template
- `cmd/cllama-passthrough/main.go:52-68` — wiring

## Future (out of scope)

- Budget caps / alerts (hard kill or soft warning per agent)
- Cost anomaly detection (spending spike = stuck loop)
- Persistent cost storage (SQLite or append-only file)
- Custom pricing overrides via `pricing.json`
- Historical cost trends / time-windowed views
- OpenRouter per-model pricing auto-fetch
