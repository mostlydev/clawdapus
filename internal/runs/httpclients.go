package runs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// CllamaRevoker calls cllama's operator revoke endpoint (cllama#54) on the
// published UI listener with the pod's CLLAMA_UI_TOKEN.
type CllamaRevoker struct {
	PodDir string
	Client *http.Client
}

func (r *CllamaRevoker) Revoke(ctx context.Context, agentID string, generation uint64) error {
	base, err := r.apiURL()
	if err != nil {
		return err
	}
	token, err := readRegularFile(filepath.Join(r.PodDir, ".claw-auth", "ui-token"), 4096)
	if err != nil {
		return errors.New("cllama operator token unavailable")
	}
	body, _ := json.Marshal(map[string]uint64{"generation": generation})
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/internal/agents/"+url.PathEscape(agentID)+"/revoke", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client().Do(req)
	if err != nil {
		return errors.New("cllama unreachable")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		// 409 (a higher generation is stored) is not an acknowledgement: that
		// revoke may still be draining. Only a terminal 200 proves the fence.
		return fmt.Errorf("cllama revoke returned %d", resp.StatusCode)
	}
	var ack struct {
		AgentID    string `json:"agent_id"`
		Generation uint64 `json:"generation"`
		State      string `json:"state"`
	}
	if err := json.Unmarshal(raw, &ack); err != nil || ack.AgentID != agentID || ack.Generation < generation || ack.State != "revoked" {
		return errors.New("cllama revoke response is not a valid acknowledgement")
	}
	return nil
}

func (r *CllamaRevoker) apiURL() (string, error) {
	names, err := ListTemplates(r.PodDir)
	if err != nil || len(names) == 0 {
		return "", errors.New("no compiled templates name a cllama endpoint")
	}
	tpl, _, err := LoadTemplate(r.PodDir, names[0])
	if err != nil {
		return "", err
	}
	return strings.TrimRight(tpl.CllamaAPIURL, "/"), nil
}

func (r *CllamaRevoker) client() *http.Client {
	if r.Client != nil {
		return r.Client
	}
	return &http.Client{CheckRedirect: noRedirects}
}

func noRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// HTTPNotifier delivers the Clawdapus run lifecycle callback contract:
//
//	POST {origin}{path}/start      {installation, run_id, generation}
//	POST {origin}{path}/heartbeat  {installation, run_id, generation}
//	POST {origin}{path}/end        {installation, run_id, generation, outcome, reason, summary}
//
// path comes from supervisor configuration with "{run_id}" substituted. The
// origin is fixed by configuration, never by the caller, redirects are not
// followed, and the held credential is sent only as a bearer header.
type HTTPNotifier struct {
	Origin       *url.URL
	PathTemplate string
	Client       *http.Client
	MaxSummary   int
}

// NewHTTPNotifier validates the configured origin.
func NewHTTPNotifier(origin, pathTemplate string) (*HTTPNotifier, error) {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
		return nil, fmt.Errorf("callback origin must be scheme://host[:port] with no path, query or userinfo")
	}
	if !strings.HasPrefix(pathTemplate, "/") || !strings.Contains(pathTemplate, "{run_id}") {
		return nil, fmt.Errorf("callback path must start with / and contain {run_id}")
	}
	u.Path = ""
	return &HTTPNotifier{
		Origin:       u,
		PathTemplate: pathTemplate,
		Client:       &http.Client{Timeout: 10 * time.Second, CheckRedirect: noRedirects},
		MaxSummary:   64 << 10,
	}, nil
}

func (n *HTTPNotifier) Started(ctx context.Context, rec *Record, credential string) error {
	return n.post(ctx, rec, credential, "start", nil)
}

func (n *HTTPNotifier) Heartbeat(ctx context.Context, rec *Record, credential string) error {
	return n.post(ctx, rec, credential, "heartbeat", nil)
}

func (n *HTTPNotifier) Ended(ctx context.Context, rec *Record, credential, outcome, reason string, summary []byte) error {
	if len(summary) > n.MaxSummary {
		summary = summary[:n.MaxSummary]
	}
	return n.post(ctx, rec, credential, "end", map[string]any{"outcome": outcome, "reason": reason, "summary": string(summary)})
}

func (n *HTTPNotifier) post(ctx context.Context, rec *Record, credential, verb string, extra map[string]any) error {
	body := map[string]any{"installation": rec.Key.Installation, "run_id": rec.Key.RunID, "generation": rec.Generation}
	for k, v := range extra {
		body[k] = v
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	target := *n.Origin
	target.Path = strings.ReplaceAll(n.PathTemplate, "{run_id}", url.PathEscape(rec.Key.RunID)) + "/" + verb
	if target.Scheme != n.Origin.Scheme || target.Host != n.Origin.Host {
		return errors.New("callback target escaped the configured origin")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.Client.Do(req)
	if err != nil {
		return errors.New("callback unreachable")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return ErrAuthority
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	}
	return fmt.Errorf("callback returned %d", resp.StatusCode)
}
