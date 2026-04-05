package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	schedulepkg "github.com/mostlydev/clawdapus/internal/schedule"
)

type scheduleControlSource interface {
	List(ctx context.Context) ([]scheduleInvocationView, error)
	Get(ctx context.Context, id string) (scheduleInvocationView, error)
	Pause(ctx context.Context, id, until, reason string) error
	Resume(ctx context.Context, id string) error
	SkipNext(ctx context.Context, id string) error
	ClearSkipNext(ctx context.Context, id string) error
	Fire(ctx context.Context, id string, bypassWhen, bypassPause bool) error
}

type scheduleInvocationView struct {
	schedulepkg.ManifestInvocation
	State schedulepkg.InvocationState `json:"state"`
}

type scheduleHTTPClient struct {
	baseURL string
	token   string
	client  *http.Client
}

type scheduleListResponse struct {
	Invocations []scheduleInvocationView `json:"invocations"`
}

type schedulePauseRequest struct {
	Until  string `json:"until,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type scheduleFireRequest struct {
	BypassWhen  bool `json:"bypass_when,omitempty"`
	BypassPause bool `json:"bypass_pause,omitempty"`
}

func newScheduleHTTPClient(baseURL, token string) scheduleControlSource {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	token = strings.TrimSpace(token)
	if baseURL == "" || token == "" {
		return nil
	}
	return &scheduleHTTPClient{
		baseURL: baseURL,
		token:   token,
		client: &http.Client{
			Timeout: 4 * time.Second,
		},
	}
}

func (c *scheduleHTTPClient) List(ctx context.Context) ([]scheduleInvocationView, error) {
	var payload scheduleListResponse
	if err := c.do(ctx, http.MethodGet, "/schedule", nil, &payload); err != nil {
		return nil, err
	}
	return payload.Invocations, nil
}

func (c *scheduleHTTPClient) Get(ctx context.Context, id string) (scheduleInvocationView, error) {
	var payload struct {
		Invocation scheduleInvocationView `json:"invocation"`
	}
	if err := c.do(ctx, http.MethodGet, scheduleInvocationPath(id, ""), nil, &payload); err != nil {
		return scheduleInvocationView{}, err
	}
	return payload.Invocation, nil
}

func (c *scheduleHTTPClient) Pause(ctx context.Context, id, until, reason string) error {
	return c.do(ctx, http.MethodPost, scheduleInvocationPath(id, "pause"), schedulePauseRequest{
		Until:  strings.TrimSpace(until),
		Reason: strings.TrimSpace(reason),
	}, nil)
}

func (c *scheduleHTTPClient) Resume(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, scheduleInvocationPath(id, "resume"), nil, nil)
}

func (c *scheduleHTTPClient) SkipNext(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, scheduleInvocationPath(id, "skip-next"), nil, nil)
}

func (c *scheduleHTTPClient) ClearSkipNext(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, scheduleInvocationPath(id, "clear-skip-next"), nil, nil)
}

func (c *scheduleHTTPClient) Fire(ctx context.Context, id string, bypassWhen, bypassPause bool) error {
	var body any
	if bypassWhen || bypassPause {
		body = scheduleFireRequest{
			BypassWhen:  bypassWhen,
			BypassPause: bypassPause,
		}
	}
	return c.do(ctx, http.MethodPost, scheduleInvocationPath(id, "fire"), body, nil)
}

func (c *scheduleHTTPClient) do(ctx context.Context, method, path string, body any, dst any) error {
	if c == nil {
		return fmt.Errorf("schedule client unavailable")
	}
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal schedule request: %w", err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return decodeScheduleAPIError(resp)
	}
	if dst == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("decode schedule response: %w", err)
	}
	return nil
}

func scheduleInvocationPath(id, action string) string {
	path := "/schedule/" + url.PathEscape(strings.TrimSpace(id))
	if strings.TrimSpace(action) != "" {
		path += "/" + action
	}
	return path
}

func decodeScheduleAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && strings.TrimSpace(payload.Error) != "" {
		return fmt.Errorf("%s", payload.Error)
	}
	if message := strings.TrimSpace(string(body)); message != "" {
		return fmt.Errorf("schedule api %s: %s", resp.Status, message)
	}
	return fmt.Errorf("schedule api %s", resp.Status)
}
