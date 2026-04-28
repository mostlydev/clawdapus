package mcpdiscover

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const DefaultProtocolVersion = "2025-11-25"

type Client struct {
	HTTPClient *http.Client
	BaseURL    string
	Path       string
	AuthToken  string
}

type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"inputSchema,omitempty"`
	Annotations map[string]interface{} `json:"annotations,omitempty"`
}

type Result struct {
	ProtocolVersion string
	Tools           []Tool
}

type initializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
}

type listToolsResult struct {
	Tools []Tool `json:"tools"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

func (c Client) Discover(ctx context.Context) (*Result, error) {
	endpoint, err := c.endpoint()
	if err != nil {
		return nil, err
	}

	initBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": DefaultProtocolVersion,
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "claw-discover",
				"version": "dev",
			},
		},
	}
	initResp, err := c.postRPC(ctx, endpoint, "", initBody, http.StatusOK)
	if err != nil {
		return nil, fmt.Errorf("mcp initialize: %w", err)
	}
	sessionID := strings.TrimSpace(initResp.Header.Get("MCP-Session-Id"))
	if sessionID == "" {
		_ = initResp.Body.Close()
		return nil, fmt.Errorf("mcp initialize: response missing MCP-Session-Id header")
	}
	var initParsed initializeResult
	if err := decodeRPCResult(initResp.Body, &initParsed); err != nil {
		return nil, fmt.Errorf("mcp initialize: %w", err)
	}

	initializedBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}
	initializedResp, err := c.postRPC(ctx, endpoint, sessionID, initializedBody, http.StatusAccepted)
	if err != nil {
		return nil, fmt.Errorf("mcp initialized notification: %w", err)
	}
	_ = initializedResp.Body.Close()

	toolsBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	}
	toolsResp, err := c.postRPC(ctx, endpoint, sessionID, toolsBody, http.StatusOK)
	if err != nil {
		return nil, fmt.Errorf("mcp tools/list: %w", err)
	}
	var toolsParsed listToolsResult
	if err := decodeRPCResult(toolsResp.Body, &toolsParsed); err != nil {
		return nil, fmt.Errorf("mcp tools/list: %w", err)
	}

	protocolVersion := strings.TrimSpace(initParsed.ProtocolVersion)
	if protocolVersion == "" {
		protocolVersion = DefaultProtocolVersion
	}
	return &Result{ProtocolVersion: protocolVersion, Tools: toolsParsed.Tools}, nil
}

func (c Client) endpoint() (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if baseURL == "" {
		return "", fmt.Errorf("mcp discover: base URL is required")
	}
	path := strings.TrimSpace(c.Path)
	if path == "" {
		path = "/mcp"
	}
	if !strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("mcp discover: path %q must start with '/'", path)
	}
	return baseURL + path, nil
}

func (c Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func (c Client) postRPC(ctx context.Context, endpoint, sessionID string, payload interface{}, wantStatus int) (*http.Response, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("MCP-Session-Id", sessionID)
	}
	if token := strings.TrimSpace(c.AuthToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != wantStatus {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("unexpected status %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return resp, nil
}

func decodeRPCResult(body io.ReadCloser, out interface{}) error {
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, 4<<20))
	if err != nil {
		return err
	}
	var resp rpcResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("parse JSON-RPC response: %w", err)
	}
	if resp.Error != nil {
		message := strings.TrimSpace(resp.Error.Message)
		if message == "" {
			message = "MCP server returned an error"
		}
		return fmt.Errorf("%s (code %d)", message, resp.Error.Code)
	}
	if len(resp.Result) == 0 {
		return fmt.Errorf("JSON-RPC response missing result")
	}
	if err := json.Unmarshal(resp.Result, out); err != nil {
		return fmt.Errorf("parse JSON-RPC result: %w", err)
	}
	return nil
}

func WaitHealth(ctx context.Context, httpClient *http.Client, baseURL string, timeout time.Duration) error {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return fmt.Errorf("health check base URL is required")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
		if err != nil {
			return err
		}
		resp, err := httpClient.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("status %s", resp.Status)
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return fmt.Errorf("health check timed out after %s: %w", timeout, lastErr)
			}
			return fmt.Errorf("health check timed out after %s", timeout)
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
