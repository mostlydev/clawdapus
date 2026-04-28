package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBridgeForwardsInitializeNotificationAndToolCall(t *testing.T) {
	server, cleanup := newTestWrapper(t, "")
	defer cleanup()

	initResp, sessionID := postRPC(t, server.URL, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`)
	if sessionID == "" {
		t.Fatal("initialize did not return MCP-Session-Id")
	}
	assertJSONField(t, initResp, "id", float64(1))
	assertJSONField(t, initResp, "result.serverInfo.name", "helper")

	status := postRPCStatus(t, server.URL, sessionID, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if status != http.StatusAccepted {
		t.Fatalf("initialized notification status = %d, want %d", status, http.StatusAccepted)
	}

	toolResp, _ := postRPC(t, server.URL, sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"message":"hello"}}}`)
	assertJSONField(t, toolResp, "id", float64(2))
	assertJSONField(t, toolResp, "result.content.0.text", "hello")
}

func TestBridgeRoutesConcurrentDuplicateClientIDs(t *testing.T) {
	server, cleanup := newTestWrapper(t, "")
	defer cleanup()
	_, sessionID := postRPC(t, server.URL, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	for _, message := range []string{"slow", "fast"} {
		wg.Add(1)
		go func(message string) {
			defer wg.Done()
			body := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"message":%q}}}`, message)
			resp, _ := postRPC(t, server.URL, sessionID, body)
			if got := jsonPath(resp, "result.content.0.text"); got != message {
				errCh <- fmt.Errorf("message %q routed to %v", message, got)
			}
			if got := jsonPath(resp, "id"); got != float64(2) {
				errCh <- fmt.Errorf("message %q response id = %v", message, got)
			}
		}(message)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

func TestBridgeRejectsUnknownSession(t *testing.T) {
	server, cleanup := newTestWrapper(t, "")
	defer cleanup()

	status := postRPCStatus(t, server.URL, "missing", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{}}`)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", status, http.StatusNotFound)
	}
}

func TestBridgeOptionalBearerAuth(t *testing.T) {
	server, cleanup := newTestWrapper(t, "secret")
	defer cleanup()

	req, err := http.NewRequest(http.MethodPost, mcpURL(server.URL), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status without auth = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}

	req, err = http.NewRequest(http.MethodPost, mcpURL(server.URL), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status with auth = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func newTestWrapper(t *testing.T, authToken string) (*httptest.Server, func()) {
	t.Helper()
	cfg := config{
		Command:        os.Args[0],
		Args:           []string{"-test.run=TestHelperProcess", "--", "stdio-helper"},
		MCPPath:        "/mcp",
		AuthToken:      authToken,
		RequestTimeout: 5 * time.Second,
		RestartBackoff: time.Hour,
		RestartMax:     time.Hour,
		MaxBodyBytes:   defaultMaxBodyBytes,
	}
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	ctx := testContext(t)
	bridge := newStdioBridge(ctx, cfg)
	go bridge.Run()
	waitForAvailable(t, bridge)

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", bridge.handleMCP)
	mux.HandleFunc("/healthz", bridge.handleHealth)
	server := httptest.NewServer(mux)
	cleanup := func() {
		server.Close()
		bridge.cancel()
	}
	return server, cleanup
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

func waitForAvailable(t *testing.T, bridge *stdioBridge) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if bridge.isAvailable() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("bridge did not become available")
}

func postRPC(t *testing.T, url string, sessionID string, body string) ([]byte, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, mcpURL(url), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if sessionID != "" {
		req.Header.Set("MCP-Session-Id", sessionID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, data)
	}
	return data, resp.Header.Get("MCP-Session-Id")
}

func postRPCStatus(t *testing.T, url string, sessionID string, body string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, mcpURL(url), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if sessionID != "" {
		req.Header.Set("MCP-Session-Id", sessionID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func mcpURL(base string) string {
	return strings.TrimRight(base, "/") + "/mcp"
}

func assertJSONField(t *testing.T, data []byte, path string, want any) {
	t.Helper()
	if got := jsonPath(data, path); got != want {
		t.Fatalf("%s = %v, want %v\n%s", path, got, want, data)
	}
}

func jsonPath(data []byte, path string) any {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil
	}
	current := value
	for _, part := range strings.Split(path, ".") {
		switch node := current.(type) {
		case map[string]any:
			current = node[part]
		case []any:
			var idx int
			fmt.Sscanf(part, "%d", &idx)
			if idx < 0 || idx >= len(node) {
				return nil
			}
			current = node[idx]
		default:
			return nil
		}
	}
	return current
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	if len(os.Args) == 0 || os.Args[len(os.Args)-1] != "stdio-helper" {
		os.Exit(2)
	}

	scanner := bufio.NewScanner(os.Stdin)
	var writeMu sync.Mutex
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		var req struct {
			ID     json.RawMessage `json:"id,omitempty"`
			Method string          `json:"method"`
			Params struct {
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		if len(bytes.TrimSpace(req.ID)) == 0 {
			continue
		}
		go func(req struct {
			ID     json.RawMessage `json:"id,omitempty"`
			Method string          `json:"method"`
			Params struct {
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}) {
			message, _ := req.Params.Arguments["message"].(string)
			if message == "slow" {
				time.Sleep(100 * time.Millisecond)
			}
			var resp map[string]any
			switch req.Method {
			case "initialize":
				resp = map[string]any{
					"jsonrpc": "2.0",
					"id":      json.RawMessage(req.ID),
					"result": map[string]any{
						"protocolVersion": "2025-11-25",
						"serverInfo":      map[string]any{"name": "helper", "version": "test"},
						"capabilities":    map[string]any{"tools": map[string]any{}},
					},
				}
			case "tools/call":
				resp = map[string]any{
					"jsonrpc": "2.0",
					"id":      json.RawMessage(req.ID),
					"result": map[string]any{
						"content": []map[string]any{{"type": "text", "text": message}},
					},
				}
			default:
				resp = map[string]any{
					"jsonrpc": "2.0",
					"id":      json.RawMessage(req.ID),
					"error":   map[string]any{"code": -32601, "message": "method not found"},
				}
			}
			data, _ := json.Marshal(resp)
			writeMu.Lock()
			fmt.Println(string(data))
			writeMu.Unlock()
		}(req)
	}
	os.Exit(0)
}
