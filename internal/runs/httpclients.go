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
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// CllamaRevoker calls cllama's operator revoke endpoint (cllama#54) on the
// published UI listener with the pod's CLLAMA_UI_TOKEN.
type CllamaRevoker struct {
	PodDir string
	Client *http.Client
}

// RevokeReport is what cllama says about an identity when it acknowledges
// its revocation. Every field is empty when the proxy predates the report.
type RevokeReport struct {
	// BootID names the proxy process that answered.
	BootID string
	// ServedBy counts the identity's served requests by credential class
	// ("subscription", "api_key"), as held in memory by that process.
	ServedBy map[string]int
	// PlanFailure is the identity's most recent plan-route failure reason.
	PlanFailure string
	// Partial is set when any part of the report was malformed and dropped.
	// What remains is then a subset and must not be presented as complete.
	Partial bool
}

// ReportingRevoker is optionally implemented by a Revoker whose
// acknowledgement carries a RevokeReport.
type ReportingRevoker interface {
	RevokeWithReport(ctx context.Context, agentID string, generation uint64) (*RevokeReport, error)
}

var (
	credentialClassRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	planFailureRe     = regexp.MustCompile(`^subscription_[a-z_]{1,48}$`)
	bootIDRe          = regexp.MustCompile(`^[0-9a-f]{16,64}$`)
)

func (r *CllamaRevoker) Revoke(ctx context.Context, agentID string, generation uint64) error {
	_, err := r.RevokeWithReport(ctx, agentID, generation)
	return err
}

func (r *CllamaRevoker) operator() (string, string, error) {
	base, err := r.apiURL()
	if err != nil {
		return "", "", err
	}
	token, err := readRegularFile(filepath.Join(r.PodDir, ".claw-auth", "ui-token"), 4096)
	if err != nil {
		return "", "", errors.New("cllama operator token unavailable")
	}
	return base, strings.TrimSpace(string(token)), nil
}

func (r *CllamaRevoker) RevokeWithReport(ctx context.Context, agentID string, generation uint64) (*RevokeReport, error) {
	base, token, err := r.operator()
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]uint64{"generation": generation})
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/internal/agents/"+url.PathEscape(agentID)+"/revoke", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client().Do(req)
	if err != nil {
		return nil, errors.New("cllama unreachable")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		// 409 (a higher generation is stored) is not an acknowledgement: that
		// revoke may still be draining. Only a terminal 200 proves the fence.
		return nil, fmt.Errorf("cllama revoke returned %d", resp.StatusCode)
	}
	var ack struct {
		AgentID     string         `json:"agent_id"`
		Generation  uint64         `json:"generation"`
		State       string         `json:"state"`
		BootID      string         `json:"boot_id"`
		ServedBy    map[string]int `json:"served_by"`
		PlanFailure string         `json:"last_plan_failure"`
	}
	if err := json.Unmarshal(raw, &ack); err != nil || ack.AgentID != agentID || ack.Generation < generation || ack.State != "revoked" {
		return nil, errors.New("cllama revoke response is not a valid acknowledgement")
	}
	// The report is advisory. Anything malformed in it is dropped, never
	// passed on, and never turns a valid acknowledgement into a failure.
	report := &RevokeReport{}
	if bootIDRe.MatchString(ack.BootID) {
		report.BootID = ack.BootID
	} else if ack.BootID != "" {
		report.Partial = true
	}
	for class, n := range ack.ServedBy {
		if !credentialClassRe.MatchString(class) || n <= 0 || len(report.ServedBy) >= 8 {
			report.Partial = true
			continue
		}
		if report.ServedBy == nil {
			report.ServedBy = map[string]int{}
		}
		report.ServedBy[class] = n
	}
	if planFailureRe.MatchString(ack.PlanFailure) {
		report.PlanFailure = ack.PlanFailure
	} else if ack.PlanFailure != "" {
		report.Partial = true
	}
	return report, nil
}

// SubscriptionStatus is one plan sign-in's state as cllama reports it. It
// never carries a token.
type SubscriptionStatus struct {
	Provider     string `json:"provider"`
	State        string `json:"state"`
	Account      string `json:"account,omitempty"`
	Reason       string `json:"reason,omitempty"`
	LimitedUntil string `json:"limited_until,omitempty"`
}

// ErrSubscriptionsUnsupported means the pod's cllama has no plan routes.
var ErrSubscriptionsUnsupported = errors.New("the pod's cllama does not report plan sign-ins")

var subscriptionStateRe = regexp.MustCompile(`^(signed_in|not_signed_in|signin_required)$`)

// Subscriptions asks cllama for the state of its plan sign-ins.
func (r *CllamaRevoker) Subscriptions(ctx context.Context) ([]SubscriptionStatus, error) {
	base, token, err := r.operator()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/subscriptions", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := r.client().Do(req)
	if err != nil {
		return nil, errors.New("cllama unreachable")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrSubscriptionsUnsupported
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cllama subscriptions returned %d", resp.StatusCode)
	}
	var body struct {
		Subscriptions []SubscriptionStatus `json:"subscriptions"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || body.Subscriptions == nil {
		return nil, ErrSubscriptionsUnsupported
	}
	out := make([]SubscriptionStatus, 0, len(body.Subscriptions))
	for _, st := range body.Subscriptions {
		if !credentialClassRe.MatchString(st.Provider) || !subscriptionStateRe.MatchString(st.State) {
			continue
		}
		// Only fixed-vocabulary fields and a bounded account label pass.
		st.Account = boundedLabel(st.Account, 128)
		st.Reason = boundedLabel(st.Reason, 128)
		if _, err := time.Parse(time.RFC3339, st.LimitedUntil); err != nil {
			st.LimitedUntil = ""
		}
		out = append(out, st)
	}
	return out, nil
}

// boundedLabel keeps a short printable label and drops anything else.
func boundedLabel(s string, max int) string {
	if len(s) > max || !utf8.ValidString(s) {
		return ""
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	return s
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
	truncated := rec.Result != nil && rec.Result.Truncated
	if len(summary) > n.MaxSummary {
		summary = summary[:n.MaxSummary]
		for len(summary) > 0 && !utf8.Valid(summary) {
			summary = summary[:len(summary)-1]
		}
		truncated = true
	}
	body := map[string]any{"outcome": outcome, "reason": reason, "summary": string(summary), "summary_truncated": truncated}
	// A task-home result also says whether it is an answer or a question, and
	// which harness session holds the turn (#396). Both values were validated
	// from the result header and stored on the record; they are sent together
	// or not at all, so a stateless run's body is unchanged.
	if r := rec.Result; r != nil && (r.Outcome == OutcomeAnswer || r.Outcome == OutcomeQuestion) && sessionIDRe.MatchString(r.Session) {
		body["result_outcome"] = r.Outcome
		body["session"] = r.Session
	}
	// What cllama reported about the run's model access, for the task-home
	// profile only: a stateless run's body is unchanged whatever the proxy
	// reports.
	if sv := rec.Served; sv != nil && rec.HomeHash != "" {
		if len(sv.By) > 0 {
			body["served_by"] = sv.By
			body["served_by_complete"] = sv.Complete
		}
		// Attempt history, not the run's result: a later entry of the chain
		// may have served the request that this route could not.
		if sv.PlanFailure != "" {
			body["last_plan_failure"] = sv.PlanFailure
		}
	}
	return n.post(ctx, rec, credential, "end", body)
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
