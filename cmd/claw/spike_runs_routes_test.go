//go:build spike

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mostlydev/clawdapus/internal/infraimages"
	"github.com/mostlydev/clawdapus/internal/runs"
)

// TestSpikeTaskHomeRoutes is the live acceptance for model routes on the
// task-home profile (#398): a plan sign-in route with an API-key fallback, a
// plan-only route, OpenAI direct and Vercel AI Gateway, each on a real Hermes
// worker through cllama built from the submodule.
//
// No agent signs in, and the plan endpoint is fixed inside cllama. To prove
// plan serving through the real worker, proxy and network path anyway, the
// fixture stands in for that endpoint: the fake provider answers TLS for
// api.openai.com on the pod's isolated network, cllama trusts only the test's
// own certificate authority, and the sign-in record holds synthetic tokens.
// If the stand-in were ever bypassed, TLS to the real host would fail before
// any request was sent.
func TestSpikeTaskHomeRoutes(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker not available: %v", err)
	}
	if os.Getenv("CLAW_SPIKE_PINNED_CLLAMA") == "1" {
		t.Skip("needs cllama with plan routes; run against the submodule build")
	}
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot, _ := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", ".."))

	workDir := t.TempDir()
	var sup *runsSpikeSupervisor
	generatedPath := filepath.Join(workDir, "compose.generated.yml")
	podFile := filepath.Join(workDir, "claw-pod.yml")
	t.Cleanup(func() {
		if sup != nil {
			sup.stop()
		}
		if t.Failed() {
			out, _ := exec.Command("docker", "compose", "-f", generatedPath, "logs", "--tail", "120").CombinedOutput()
			t.Logf("compose logs:\n%s", out)
		}
		spikeRunsCleanup(t, workDir, generatedPath)
	})

	unique := fmt.Sprintf("issue398-spike-%d", time.Now().UnixNano())
	prevTag := infraCllamaTag
	infraCllamaTag = unique
	t.Cleanup(func() { infraCllamaTag = prevTag })
	cllamaRef := "ghcr.io/mostlydev/cllama:" + unique
	spikeBuildImage(t, filepath.Join(repoRoot, "cllama"), cllamaRef, "Dockerfile")
	t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", "-f", cllamaRef).Run() })

	pythonImage := "python:3.12-alpine"
	spikeEnsurePulledImage(t, pythonImage)
	hermesBase := "ghcr.io/mostlydev/hermes-base:" + infraimages.DefaultHermesBaseTag
	spikeEnsurePulledImage(t, hermesBase)
	workerImage := "claw-routes-spike-worker:" + unique
	workerCtx := filepath.Join(workDir, "worker-image")
	if err := os.MkdirAll(workerCtx, 0o755); err != nil {
		t.Fatal(err)
	}
	spikeWriteFile(t, filepath.Join(workerCtx, "Dockerfile"), "FROM "+hermesBase+"\nLABEL claw.type=hermes\n")
	spikeBuildImage(t, workerCtx, workerImage, "Dockerfile")
	t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", "-f", workerImage).Run() })

	spikeWriteFile(t, filepath.Join(workDir, "AGENTS.md"), "# Task worker\n\nDo the task.")
	providerImage := "claw-routes-spike-provider:" + unique
	providerCtx := filepath.Join(workDir, "provider-image")
	if err := os.MkdirAll(providerCtx, 0o755); err != nil {
		t.Fatal(err)
	}
	spikeWriteFile(t, filepath.Join(providerCtx, "fake_provider.py"), routesSpikeProvider)
	caPEM, certPEM, keyPEM := routesSpikeCerts(t, "api.openai.com")
	spikeWriteFile(t, filepath.Join(providerCtx, "plan.crt"), certPEM)
	spikeWriteFile(t, filepath.Join(providerCtx, "plan.key"), keyPEM)
	// cllama reads its trust roots from its own auth mount, and nowhere else.
	authDir := filepath.Join(workDir, ".claw-auth")
	if err := os.MkdirAll(authDir, 0o777); err != nil {
		t.Fatal(err)
	}
	spikeWriteFile(t, filepath.Join(authDir, "test-ca.pem"), caPEM)
	spikeWriteFile(t, filepath.Join(providerCtx, "Dockerfile"), "FROM "+pythonImage+"\nCOPY fake_provider.py plan.crt plan.key /app/\nCMD [\"python\",\"-u\",\"/app/fake_provider.py\"]\n")
	spikeBuildImage(t, providerCtx, providerImage, "Dockerfile")
	t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", "-f", providerImage).Run() })

	worker := func(name, models string) string {
		return fmt.Sprintf(`  %s:
    image: %s
    x-claw:
      agent: ./AGENTS.md
      lifecycle: per-run
      models:
%s
      run:
        max-runtime: 5m
        max-authority: 30m
        home: {}
        content:
          - source: snapshots
            target: /ref
            versioned: true
`, name, workerImage, models)
	}
	for _, id := range []string{"v1", "v2"} {
		dir := filepath.Join(workDir, "snapshots", id)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		spikeWriteFile(t, filepath.Join(dir, "version.txt"), "SNAPSHOT-"+id)
		if err := os.Chmod(filepath.Join(dir, "version.txt"), 0444); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0755) })
	}
	spikeWriteFile(t, podFile, fmt.Sprintf(`name: %[1]s

x-claw:
  pod: %[1]s
  # Test fixture: fake provider endpoints cannot meet the conservative default.
  data-policy-defaults:
    regions: [global]
    training: allowed
    retention: provider-default
    local-retention: allowed
  cllama-defaults:
    proxy: [passthrough]
    env:
      OPENAI_API_KEY: sk-openai-fake-key
      OPENAI_BASE_URL: http://fake-provider:8080/v1
      AI_GATEWAY_API_KEY: gw-fake-gateway-key
      AI_GATEWAY_BASE_URL: http://fake-provider:8080/v1
      # Test fixture: trust only the test's certificate authority.
      SSL_CERT_FILE: /claw/auth/test-ca.pem
      SSL_CERT_DIR: /nonexistent

services:
%[2]s%[3]s%[4]s%[5]s
  fake-provider:
    image: %[6]s
    expose:
      - "8080"
    networks:
      claw-internal:
        aliases:
          - api.openai.com
`, "runs-spike-"+unique,
		worker("plan-first", "        primary: chatgpt/gpt-5.6-terra\n        fallback: openai/gpt-5.6-terra"),
		worker("plan-only", "        primary: chatgpt/gpt-5.6-terra"),
		worker("direct", "        primary: openai/gpt-5.6-terra"),
		worker("gateway", "        primary: vercel/openai/gpt-4.1-nano"),
		providerImage))

	t.Setenv("CLLAMA_UI_PORT", spikeFreePort(t))
	t.Setenv("CLAW_DASHBOARD_BIND", "127.0.0.1")
	t.Setenv("CLAWDASH_ENABLED", "false")
	prevDetach := composeUpDetach
	composeUpDetach = true
	defer func() { composeUpDetach = prevDetach }()
	if err := runComposeUp(podFile); err != nil {
		t.Fatalf("runComposeUp: %v", err)
	}
	spikeWaitHealthy(t, spikeComposeContainerID(t, generatedPath, "cllama"), 60*time.Second)
	runsSpikePodNS = runs.PodNamespace(workDir)

	for _, name := range []string{"plan-first", "plan-only", "direct", "gateway"} {
		tpl, _, err := runs.LoadTemplate(workDir, name)
		if err != nil {
			t.Fatalf("template %s: %v", name, err)
		}
		if tpl.ClawType != "hermes" || tpl.Worker.Home == nil {
			t.Fatalf("template %s is not a Hermes task-home template: %+v", name, tpl)
		}
	}
	// No plan credential or provider entry was compiled for the plan route.
	if raw, err := os.ReadFile(filepath.Join(workDir, ".claw-auth", "providers.json")); err == nil && strings.Contains(string(raw), "chatgpt") {
		t.Fatalf("claw up seeded a provider entry for the plan route: %s", raw)
	}

	sockDir, err := os.MkdirTemp("/tmp", "clawruns-spike-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockDir)
	_ = os.Chmod(sockDir, 0o700)
	cb := newRunsSpikeCallbacks()
	cbServer := httptest.NewServer(cb)
	defer cbServer.Close()
	sup = startRunsSpikeSupervisor(t, workDir, sockDir, cbServer.URL)
	client, err := runs.Dial(sockDir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Sign-in state is available without a run, and carries no token.
	planStatus := func() runs.SubscriptionStatus {
		t.Helper()
		var subs struct {
			Subscriptions []runs.SubscriptionStatus `json:"subscriptions"`
		}
		if err := client.Do(ctx, "GET", "/v1/subscriptions", nil, &subs); err != nil {
			t.Fatalf("subscriptions: %v", err)
		}
		if len(subs.Subscriptions) != 1 || subs.Subscriptions[0].Provider != "chatgpt" {
			t.Fatalf("subscriptions: %+v", subs.Subscriptions)
		}
		return subs.Subscriptions[0]
	}
	if st := planStatus(); st.State != "not_signed_in" {
		t.Fatalf("before any sign-in: %+v", st)
	}

	// The operator's commands work as documented: a one-off container of the
	// cllama service, through Compose.
	out, err := exec.Command("docker", "compose", "-f", generatedPath, "run", "--rm", "-T", "cllama", "auth", "status").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "chatgpt: not_signed_in") {
		t.Fatalf("auth status: %v\n%s", err, out)
	}
	// The sign-in listener is reachable on the host's loopback and refuses a
	// callback that does not carry its state. Nothing is sent to the vendor:
	// the command only prints a URL and waits.
	loginPort := spikeFreePort(t)
	login := exec.Command("docker", "compose", "-f", generatedPath, "run", "--rm", "-T", "-p", "127.0.0.1:"+loginPort+":1455",
		"cllama", "auth", "login", "chatgpt", "--listen", "0.0.0.0:1455", "--port", loginPort, "--timeout", "25s")
	var loginOut strings.Builder
	login.Stdout, login.Stderr = &loginOut, &loginOut
	if err := login.Start(); err != nil {
		t.Fatal(err)
	}
	refused := false
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline) && !refused; time.Sleep(300 * time.Millisecond) {
		resp, err := http.Get("http://127.0.0.1:" + loginPort + "/auth/callback?state=forged&code=x&client_id=oaiapp_x")
		if err == nil {
			refused = resp.StatusCode == http.StatusBadRequest
			resp.Body.Close()
		}
	}
	_ = login.Wait() // times out: nobody signs in
	if !refused || !strings.Contains(loginOut.String(), "https://auth.openai.com/api/accounts/authorize?") ||
		!strings.Contains(loginOut.String(), "redirect_uri=http%3A%2F%2F127.0.0.1%3A"+loginPort+"%2Fauth%2Fcallback") {
		t.Fatalf("sign-in command: refused=%v\n%s", refused, loginOut.String())
	}
	if st := planStatus(); st.State != "not_signed_in" {
		t.Fatalf("a refused callback changed the sign-in state: %+v", st)
	}

	launch := func(runID, template, task string, version ...string) {
		snapshot := "v1"
		if len(version) > 0 {
			snapshot = version[0]
		}
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"template": template, "task": task, "tools": []string{}, "home": "home-" + runID, "content": map[string]string{"/ref": snapshot}})
		_, _, digest, _ := runs.CanonicalSpec(raw)
		if err := client.Do(ctx, "POST", "/v1/runs", map[string]any{
			"installation": "inst", "run_id": runID, "generation": 1, "spec_digest": digest,
			"spec": json.RawMessage(raw), "authority_expires_at": time.Now().Add(20 * time.Minute),
			"credential": "cred-" + runID,
		}, nil); err != nil {
			t.Fatalf("launch %s: %v", runID, err)
		}
	}
	endBody := func(runID string) map[string]any {
		t.Helper()
		runsSpikeWaitCallback(t, cb, runID, "end", "cred-"+runID)
		cb.mu.Lock()
		raw := cb.bodies[runID+"/end"]
		cb.mu.Unlock()
		var body map[string]any
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			t.Fatalf("end callback of %s is not JSON: %q", runID, raw)
		}
		return body
	}
	answer := func(runID string) (runs.StatusView, string) {
		t.Helper()
		view := runsSpikeWait(t, client, runID, "stopped")
		if view.State != runs.StateStopped || view.Failure != "" || !view.Revoked || view.Result == nil {
			t.Fatalf("run %s: %+v", runID, view)
		}
		var body []byte
		if err := client.Do(ctx, "GET", runs.RunPath(runs.Key{Installation: "inst", RunID: runID}, "/result"), nil, &body); err != nil {
			t.Fatalf("run %s result: %v", runID, err)
		}
		return view, string(body)
	}
	providerLog := func() string {
		t.Helper()
		out, err := exec.Command("docker", "logs", spikeComposeContainerID(t, generatedPath, "fake-provider")).CombinedOutput()
		if err != nil {
			t.Fatalf("provider logs: %v", err)
		}
		return string(out)
	}
	// servedOnlyByKey checks a callback's counts: at least one request, all
	// served by an API key, from one proxy process.
	servedOnlyByKey := func(runID string, end map[string]any) {
		t.Helper()
		served, _ := end["served_by"].(map[string]any)
		n, _ := served["api_key"].(float64)
		if n < 1 || len(served) != 1 || end["served_by_complete"] != true {
			t.Fatalf("%s: served_by: %v complete=%v", runID, end["served_by"], end["served_by_complete"])
		}
	}

	// 1. OpenAI direct: the terra model goes through the Responses API with
	// the OpenAI key.
	launch("direct1", "direct", "ROUTE-TASK-DIRECT")
	if _, body := answer("direct1"); body != "ANSWER route=responses model=gpt-5.6-terra key=openai" {
		t.Fatalf("case 1 (direct): %q", body)
	}
	end := endBody("direct1")
	servedOnlyByKey("direct1", end)
	if _, ok := end["last_plan_failure"]; ok || end["outcome"] != "succeeded" {
		t.Fatalf("case 1 (direct): end callback: %v", end)
	}

	// 2. Vercel AI Gateway: a cheaper model, sent with the gateway key and
	// the provider-prefixed model name the gateway expects.
	launch("gw1", "gateway", "ROUTE-TASK-GATEWAY")
	if _, body := answer("gw1"); body != "ANSWER route=chat model=openai/gpt-4.1-nano key=gateway" {
		t.Fatalf("case 2 (gateway): %q", body)
	}
	servedOnlyByKey("gw1", endBody("gw1"))

	// 3. Plan first, no sign-in: the API key serves, and the callback says
	// both that it did and why the plan did not.
	launch("pf1", "plan-first", "ROUTE-TASK-PLANFIRST")
	if _, body := answer("pf1"); body != "ANSWER route=responses model=gpt-5.6-terra key=openai" {
		t.Fatalf("case 3 (plan first): %q", body)
	}
	end = endBody("pf1")
	servedOnlyByKey("pf1", end)
	if end["last_plan_failure"] != "subscription_signin_required" || end["outcome"] != "succeeded" {
		t.Fatalf("case 3 (plan first): end callback: %v", end)
	}

	// 4. Plan only, no sign-in: the run fails, the callback names the plan
	// reason, nothing is counted as served.
	launch("po1", "plan-only", "ROUTE-TASK-PLANONLY")
	view := runsSpikeWait(t, client, "po1", "stopped")
	if !view.State.Terminal() || !view.Revoked || (view.Failure == "" && view.Result != nil && view.Result.Outcome == runs.OutcomeAnswer) {
		t.Fatalf("case 4 (plan only) must not succeed: %+v", view)
	}
	end = endBody("po1")
	if end["last_plan_failure"] != "subscription_signin_required" || end["outcome"] == "succeeded" {
		t.Fatalf("case 4 (plan only): end callback: %v", end)
	}
	if _, ok := end["served_by"]; ok {
		t.Fatalf("case 4 (plan only): nothing was served, yet: %v", end["served_by"])
	}
	if view.Served == nil || view.Served.PlanFailure != "subscription_signin_required" || len(view.Served.By) != 0 {
		t.Fatalf("case 4 (plan only): status: %+v", view.Served)
	}

	// Negative control: the plan-only task never reached the provider, with
	// any key. Positive control: the same log does show the other tasks, so
	// the probe would have seen it.
	logs := providerLog()
	for _, marker := range []string{"ROUTE-TASK-DIRECT", "ROUTE-TASK-GATEWAY", "ROUTE-TASK-PLANFIRST"} {
		if !strings.Contains(logs, marker) {
			t.Fatalf("provider log lacks %s; the negative control proves nothing:\n%s", marker, logs)
		}
	}
	if strings.Contains(logs, "ROUTE-TASK-PLANONLY") {
		t.Fatalf("a plan-only run reached a keyed provider:\n%s", logs)
	}
	if !strings.Contains(logs, "key=openai") || !strings.Contains(logs, "key=gateway") {
		t.Fatalf("provider log does not show both keys in use:\n%s", logs)
	}

	// From here on a sign-in exists. Its tokens are synthetic and are
	// written by a helper container as the proxy's own user, the way the
	// sign-in command would write them.
	const planToken = "plan-synthetic-access-canary"
	record, _ := json.Marshal(map[string]any{
		"provider": "chatgpt", "client_id": "oaiapp_fixture", "host_id": "urn:uuid:00000000-0000-4000-8000-000000000000",
		"subject": "fixture", "email": "fixture@example.com", "access_token": planToken, "refresh_token": "plan-synthetic-refresh-canary",
		"scopes": []string{"offline_access", "chatgpt.tokens.use.direct"}, "expires_at": time.Now().Add(24 * time.Hour).UTC(), "state": "active",
	})
	seed := exec.Command("docker", "run", "--rm", "-i", "-v", authDir+":/claw/auth", pythonImage, "python", "-c",
		"import os,sys\nd='/claw/auth/subscriptions'\nos.makedirs(d,0o700,exist_ok=True)\nos.chmod(d,0o700)\nfd=os.open(d+'/chatgpt.json',os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o600)\nos.write(fd,sys.stdin.buffer.read())\nos.close(fd)")
	seed.Stdin = strings.NewReader(string(record))
	if out, err := seed.CombinedOutput(); err != nil {
		t.Fatalf("seed synthetic sign-in: %v\n%s", err, out)
	}
	if st := planStatus(); st.State != "signed_in" || st.Account != "fixture@example.com" {
		t.Fatalf("after the sign-in: %+v", st)
	}

	// 5. Plan first, signed in: the plan serves through the real worker and
	// proxy path, and the callback counts only the plan.
	launch("pf2", "plan-first", "ROUTE-TASK-PLANSERVED")
	if _, body := answer("pf2"); body != "ANSWER route=responses model=gpt-5.6-terra key=plan" {
		t.Fatalf("case 5 (plan serves): %q", body)
	}
	end = endBody("pf2")
	served, _ := end["served_by"].(map[string]any)
	if n, _ := served["subscription"].(float64); n < 1 || len(served) != 1 || end["served_by_complete"] != true {
		t.Fatalf("case 5 (plan serves): served_by: %v", end)
	}
	if _, ok := end["last_plan_failure"]; ok {
		t.Fatalf("case 5 (plan serves): end callback: %v", end)
	}

	// 6. Plan only, signed in: the same template that failed in case 4.
	launch("po2", "plan-only", "ROUTE-TASK-PLANONLY-SIGNEDIN")
	if _, body := answer("po2"); body != "ANSWER route=responses model=gpt-5.6-terra key=plan" {
		t.Fatalf("case 6 (plan only, signed in): %q", body)
	}

	// The plan route was shaped as the vendor requires, for a real worker's
	// request: stream, no storage, tools in one namespace, no system-role
	// item and no sampling or length fields.
	logs = providerLog()
	if !strings.Contains(logs, "PLANSHAPE stream=True store=False system=False namespaced=True dropped=True task='ROUTE-TASK-PLANSERVED'") {
		t.Fatalf("plan request shape:\n%s", logs)
	}

	// 7. The worker cannot see the sign-in, the operator token or a provider
	// key. The task dumps its environment, its mounts and the paths where a
	// store would be. The positive control is its own home mount and model
	// key variable, which the same dump does show.
	launch("probe1", "gateway", "RUN: env; cat /proc/mounts; ls -la / /claw /claw/auth /claw/auth/subscriptions 2>&1; echo PROBE-DONE")
	_, dump := answer("probe1")
	if !strings.Contains(dump, "PROBE-DONE") || !strings.Contains(dump, "/home/task") || !strings.Contains(dump, "CLAW_MODEL_KEY=") {
		t.Fatalf("case 7: the probe did not run as expected:\n%s", dump)
	}
	for _, secret := range []string{planToken, "plan-synthetic-refresh-canary", "sk-openai-fake-key", "gw-fake-gateway-key", "chatgpt.json", ".claw-auth", "CLLAMA_UI_TOKEN", "ui-token", "AI_GATEWAY_API_KEY", "OPENAI_API_KEY"} {
		if strings.Contains(dump, secret) {
			t.Fatalf("case 7: the worker can see %q:\n%s", secret, dump)
		}
	}

	// 8. Plan first, plan limit reached: the key serves, the callback says
	// why the plan did not, and the status shows the route resting.
	launch("pf3", "plan-first", "ROUTE-TASK-LIMIT")
	if _, body := answer("pf3"); body != "ANSWER route=responses model=gpt-5.6-terra key=openai" {
		t.Fatalf("case 8 (plan limit): %q", body)
	}
	end = endBody("pf3")
	servedOnlyByKey("pf3", end)
	if end["last_plan_failure"] != "subscription_limit_reached" || end["outcome"] != "succeeded" {
		t.Fatalf("case 8 (plan limit): end callback: %v", end)
	}
	if st := planStatus(); st.State != "signed_in" || st.LimitedUntil == "" {
		t.Fatalf("case 8 (plan limit): status: %+v", st)
	}

	// 9. Hosted capabilities. The plan route is resting after case 8, so the
	// plan-first worker is on its API-key fallback, under this pod's fully
	// permissive data policy. With its own run token the worker asks cllama
	// directly. An ordinary request is served by the API key, which shows
	// the lane is open. The same request with a vendor-hosted tool is
	// refused and never reaches the provider, and there is no way around the
	// adapter through /v1/responses.
	launch("pf4", "plan-first", "RUN: python3 -c '"+routesSpikeHostedProbe+"'")
	_, probe := answer("pf4")
	// The terminal tool reports its result as JSON; the probe's lines are
	// in its output field.
	var toolResult struct {
		Output string `json:"output"`
	}
	if json.Unmarshal([]byte(probe), &toolResult) == nil && toolResult.Output != "" {
		probe = toolResult.Output
	}
	if !strings.Contains(probe, "HOSTED-PROBE-DONE") {
		t.Fatalf("case 9: the probe did not finish:\n%s", probe)
	}
	lineOf := func(label string) string {
		for _, line := range strings.Split(probe, "\n") {
			if strings.HasPrefix(line, label+" ") {
				return line
			}
		}
		t.Fatalf("case 9: no %s line in the probe output:\n%s", label, probe)
		return ""
	}
	if line := lineOf("PLAIN"); !strings.HasPrefix(line, "PLAIN 200 ") || !strings.Contains(line, "route=responses model=gpt-5.6-terra key=openai") {
		t.Fatalf("case 9: an ordinary request was not served by the API key: %s", line)
	}
	if line := lineOf("SEARCH"); !strings.HasPrefix(line, "SEARCH 400 ") || !strings.Contains(line, "hosted capability not granted: web_search") {
		t.Fatalf("case 9: a hosted web search was not refused: %s", line)
	}
	if line := lineOf("MCP"); !strings.HasPrefix(line, "MCP 400 ") || !strings.Contains(line, "hosted capability not granted: mcp") {
		t.Fatalf("case 9: a remote MCP server was not refused: %s", line)
	}
	if line := lineOf("RESPONSES"); !strings.HasPrefix(line, "RESPONSES 404 ") {
		t.Fatalf("case 9: /v1/responses is reachable by a worker: %s", line)
	}
	logs = providerLog()
	if !strings.Contains(logs, "REQUEST route=responses model=gpt-5.6-terra key=openai tls=False task='PROBE-PLAIN-RAW'") {
		t.Fatalf("case 9: the ordinary request did not reach the provider on the API-key lane:\n%s", logs)
	}
	// The provider logs a request's task in an exact field. The outer probe
	// command names these markers too, so match the field, not the word.
	for _, marker := range []string{"task='PROBE-SEARCH-RAW'", "task='PROBE-MCP-RAW'", "task='PROBE-RESPONSES-RAW'"} {
		if strings.Contains(logs, marker) {
			t.Fatalf("case 9: a refused request reached the provider (%s):\n%s", marker, logs)
		}
	}
	servedOnlyByKey("pf4", endBody("pf4"))

	// 10. Deliverables. A real worker writes a file into its home's
	// deliverables directory with its own tools; the supervisor lists it and
	// returns its bytes, and serves nothing else from the home.
	launch("dl1", "gateway", "RUN: echo DELIVERED-BY-WORKER > /home/task/deliverables/note.txt; echo private > /home/task/workspace/private.txt; ls -ld /home/task/deliverables; echo DELIVERABLE-WRITTEN")
	if _, out := answer("dl1"); !strings.Contains(out, "DELIVERABLE-WRITTEN") {
		t.Fatalf("case 10: the worker did not write its deliverable:\n%s", out)
	}
	var listing struct {
		Deliverables []runs.Deliverable `json:"deliverables"`
		Truncated    bool               `json:"truncated"`
	}
	if err := client.Do(ctx, "GET", "/v1/homes/inst/home-dl1/deliverables", nil, &listing); err != nil {
		t.Fatalf("case 10: list: %v", err)
	}
	if len(listing.Deliverables) != 1 || listing.Deliverables[0].Name != "note.txt" || listing.Truncated {
		t.Fatalf("case 10: listing = %+v", listing)
	}
	var fetched runs.DeliverableContent
	if err := client.Do(ctx, "GET", "/v1/homes/inst/home-dl1/deliverables/note.txt?sha256="+listing.Deliverables[0].SHA256, nil, &fetched); err != nil {
		t.Fatalf("case 10: fetch: %v", err)
	}
	if raw, _ := base64.StdEncoding.DecodeString(fetched.ContentBase64); strings.TrimSpace(string(raw)) != "DELIVERED-BY-WORKER" {
		t.Fatalf("case 10: content = %q", raw)
	}
	if err := client.Do(ctx, "GET", "/v1/homes/inst/home-dl1/deliverables/private.txt", nil, &fetched); err == nil {
		t.Fatal("case 10: a workspace file was served as a deliverable")
	}

	// 11. The same compiled template selects distinct immutable references.
	for _, id := range []string{"v1", "v2"} {
		runID := "snapshot-" + id
		launch(runID, "gateway", "RUN: cat /ref/version.txt; if echo BAD > /ref/version.txt 2>/dev/null; then echo WRITABLE; fi", id)
		view, out := answer(runID)
		if !strings.Contains(out, "SNAPSHOT-"+id) || strings.Contains(out, "WRITABLE") {
			t.Fatalf("snapshot %s: %s", id, out)
		}
		if len(view.Content) != 1 || view.Content[0].Snapshot != id || view.Content[0].ManifestDigest == "" {
			t.Fatalf("snapshot status: %+v", view.Content)
		}
	}

	// The synthetic tokens appear nowhere but the sign-in record: not in the
	// proxy's log, the callbacks, or anything the pod wrote outside the
	// store. The log check has a positive control: it does name the class.
	cllamaLogs, _ := exec.Command("docker", "logs", spikeComposeContainerID(t, generatedPath, "cllama")).CombinedOutput()
	for _, refusal := range []string{"hosted_capability_refused:web_search", "hosted_capability_refused:mcp"} {
		if !strings.Contains(string(cllamaLogs), refusal) {
			t.Fatalf("cllama log does not record %s:\n%.3000s", refusal, cllamaLogs)
		}
	}
	if !strings.Contains(string(cllamaLogs), `"credential":"api_key"`) || !strings.Contains(string(cllamaLogs), `"credential":"subscription"`) {
		t.Fatalf("cllama log does not record both credential classes:\n%.3000s", cllamaLogs)
	}
	if strings.Contains(string(cllamaLogs), "synthetic") {
		t.Fatal("cllama log holds a sign-in token")
	}
	cb.mu.Lock()
	for name, body := range cb.bodies {
		if strings.Contains(body, "synthetic") {
			t.Fatalf("callback %s holds a sign-in token", name)
		}
	}
	cb.mu.Unlock()
	leaked, scanned := "", 0
	_ = filepath.WalkDir(workDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			// The store itself, and the fixture provider's own source, are
			// the two places the synthetic token is meant to be.
			if d != nil && d.IsDir() && (d.Name() == "subscriptions" || d.Name() == "provider-image") {
				return filepath.SkipDir
			}
			return nil
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		scanned++
		if strings.Contains(string(raw), "synthetic") {
			leaked = path
		}
		return nil
	})
	if leaked != "" || scanned < 10 {
		t.Fatalf("sign-in token found in %q (scanned %d files)", leaked, scanned)
	}

	if out, _ := exec.Command("docker", "ps", "-aq", "--filter", "label="+runs.LabelPodNS+"="+runsSpikePodNS).Output(); strings.TrimSpace(string(out)) != "" {
		t.Fatalf("containers left behind: %s", out)
	}
}

// routesSpikeProvider is a fake provider that answers both the chat
// completions API and the Responses API. Each answer names the route, the
// model and which key authorized it, and each request is logged with the
// task text. On the chat route a task starting with "RUN: " makes it call
// the terminal tool and return the tool's output.
const routesSpikeProvider = `import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

def text_of(content):
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return "".join(str(p.get("text", "")) for p in content if isinstance(p, dict))
    return ""

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass
    def do_GET(self):
        self.send_response(200 if self.path == "/health" else 404)
        self.end_headers()
    def send_json(self, data, ctype="application/json"):
        raw = data if isinstance(data, bytes) else json.dumps(data).encode()
        self.send_response(200)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)
    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
        auth = self.headers.get("Authorization", "")
        key = "openai" if auth == "Bearer sk-openai-fake-key" else "gateway" if auth == "Bearer gw-fake-gateway-key" else "plan" if auth == "Bearer plan-synthetic-access-canary" else "unknown"
        model = body.get("model", "")
        if self.path.endswith("/responses"):
            users = [i for i in body.get("input", []) if isinstance(i, dict) and i.get("role") == "user"]
            task = text_of(users[-1].get("content")) if users else ""
            print("REQUEST route=responses model=%s key=%s tls=%s task=%r" % (model, key, self.server.tls, task[:80]), flush=True)
            if self.server.tls:
                # The stand-in for the plan endpoint: only the plan token is
                # accepted, and a reply is an event stream.
                if key != "plan":
                    self.send_response(401)
                    self.end_headers()
                    return
                items = [i for i in body.get("input", []) if isinstance(i, dict)]
                tools = body.get("tools", [])
                print("PLANSHAPE stream=%s store=%s system=%s namespaced=%s dropped=%s task=%r" % (
                    body.get("stream"), body.get("store"), any(i.get("role") == "system" for i in items),
                    len(tools) == 1 and tools[0].get("type") == "namespace",
                    not any(k in body for k in ("temperature", "top_p", "max_output_tokens", "user", "metadata")), task[:80]), flush=True)
                if "ROUTE-TASK-LIMIT" in task:
                    raw = json.dumps({"error": {"code": "subscription_sharing_usage_limit_exceeded", "message": "limit"}}).encode()
                    self.send_response(429)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(raw)))
                    self.end_headers()
                    self.wfile.write(raw)
                    return
                done = {"type": "response.completed", "response": {"id": "resp_plan", "object": "response", "status": "completed", "model": model,
                    "output": [{"type": "message", "role": "assistant", "content": [{"type": "output_text",
                        "text": "ANSWER route=responses model=%s key=%s" % (model, key)}]}],
                    "usage": {"input_tokens": 10, "output_tokens": 2, "total_tokens": 12}}}
                self.send_json(("data: " + json.dumps({"type": "response.created"}) + "\n\ndata: " + json.dumps(done) + "\n\n").encode(), "text/event-stream")
                return
            text = "ANSWER route=responses model=%s key=%s" % (model, key)
            if "RUN: " in task:
                # The same scripted terminal call as on the chat route.
                outs = [i for i in body.get("input", []) if isinstance(i, dict) and i.get("type") == "function_call_output"]
                names = [t.get("name") for t in body.get("tools", []) if isinstance(t, dict)]
                if outs:
                    text = str(outs[-1].get("output", "")).strip() or "EMPTY-TOOL-OUTPUT"
                elif "terminal" in names:
                    self.send_json({"id": "resp_routes", "object": "response", "status": "completed", "model": model,
                        "output": [{"type": "function_call", "id": "fc_routes", "call_id": "call-routes", "name": "terminal",
                            "arguments": json.dumps({"command": task.split("RUN: ", 1)[1].split("\n\n")[0], "timeout": 120})}],
                        "usage": {"input_tokens": 10, "output_tokens": 2, "total_tokens": 12}})
                    return
                else:
                    text = "NO-TERMINAL-TOOL"
            self.send_json({"id": "resp_routes", "object": "response", "status": "completed", "model": model,
                "output": [{"type": "message", "role": "assistant", "content": [{"type": "output_text",
                    "text": text}]}],
                "usage": {"input_tokens": 10, "output_tokens": 2, "total_tokens": 12}})
            return
        messages = body.get("messages", [])
        last = messages[-1] if messages else {}
        users = [m for m in messages if m.get("role") == "user"]
        task = text_of(users[-1].get("content")) if users else ""
        print("REQUEST route=chat model=%s key=%s task=%r" % (model, key, task[:80]), flush=True)
        msg, finish = {"role": "assistant", "content": "ANSWER route=chat model=%s key=%s" % (model, key)}, "stop"
        if "RUN: " in task:
            if last.get("role") == "tool":
                msg["content"] = str(last.get("content", "")).strip() or "EMPTY-TOOL-OUTPUT"
            elif any(t.get("function", {}).get("name") == "terminal" for t in body.get("tools", [])):
                msg = {"role": "assistant", "content": None, "tool_calls": [{"id": "call-routes", "type": "function", "function": {
                    "name": "terminal", "arguments": json.dumps({"command": task.split("RUN: ", 1)[1].split("\n\n")[0], "timeout": 120})}}]}
                finish = "tool_calls"
            else:
                msg["content"] = "NO-TERMINAL-TOOL"
        data = {"id": "chatcmpl-routes", "object": "chat.completion", "created": 1, "model": model,
                "choices": [{"index": 0, "message": msg, "finish_reason": finish}],
                "usage": {"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12}}
        if body.get("stream"):
            delta = dict(msg)
            if delta.get("tool_calls"):
                delta["tool_calls"] = [dict(delta["tool_calls"][0], index=0)]
            chunks = [dict(data, object="chat.completion.chunk", choices=[{"index": 0, "delta": delta, "finish_reason": None}]),
                      dict(data, object="chat.completion.chunk", choices=[{"index": 0, "delta": {}, "finish_reason": finish}])]
            self.send_json(("".join("data: " + json.dumps(c) + "\n\n" for c in chunks) + "data: [DONE]\n\n").encode(), "text/event-stream")
        else:
            self.send_json(data)

import ssl, threading
plain = ThreadingHTTPServer(("0.0.0.0", 8080), Handler)
plain.tls = False
secure = ThreadingHTTPServer(("0.0.0.0", 443), Handler)
secure.tls = True
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain("/app/plan.crt", "/app/plan.key")
secure.socket = ctx.wrap_socket(secure.socket, server_side=True)
threading.Thread(target=secure.serve_forever, daemon=True).start()
plain.serve_forever()
`

// routesSpikeCerts makes a throwaway certificate authority and a server
// certificate for one host name, for the fixture's TLS stand-in.
func routesSpikeCerts(t *testing.T, host string) (caPEM, certPEM, keyPEM string) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "claw spike fixture CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(6 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(6 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, caTpl, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemOf := func(kind string, der []byte) string {
		return string(pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}))
	}
	return pemOf("CERTIFICATE", caDER), pemOf("CERTIFICATE", der), pemOf("PRIVATE KEY", keyDER)
}

// routesSpikeHostedProbe is run by a worker with its own run token. It asks
// cllama for an ordinary completion, the same with a vendor-hosted web
// search, the same with a remote MCP server, and the Responses API directly.
// The worker can reach cllama only through its run's gate, so the probe
// takes the address from the worker's own model configuration. It has no
// blank line and no single quote, so it survives as one command.
const routesSpikeHostedProbe = `import os, re, json, urllib.request, urllib.error
home = os.environ.get("HERMES_HOME") or "/home/task/hermes"
U = re.search(r"base_url:\s*(\S+)", open(home + "/config.yaml").read()).group(1).strip("\"").rstrip("/")
K = os.environ["CLAW_MODEL_KEY"]
M = "openai/gpt-5.6-terra"
def post(path, body):
    req = urllib.request.Request(U + path, data=json.dumps(body).encode(), headers={"Authorization": "Bearer " + K, "Content-Type": "application/json"})
    try:
        r = urllib.request.urlopen(req, timeout=60)
        return str(r.status) + " " + " ".join(r.read().decode()[:400].split())
    except urllib.error.HTTPError as e:
        return str(e.code) + " " + " ".join(e.read().decode()[:400].split())
    except Exception as e:
        return "ERR " + " ".join(repr(e)[:200].split())
def chat(task, tools=None):
    body = {"model": M, "messages": [{"role": "user", "content": task}]}
    if tools:
        body["tools"] = tools
    return post("/chat/completions", body)
print("PLAIN", chat("PROBE-PLAIN-RAW"))
print("SEARCH", chat("PROBE-SEARCH-RAW", [{"type": "web_search"}]))
print("MCP", chat("PROBE-MCP-RAW", [{"type": "mcp", "server_label": "x", "server_url": "https://mcp.example/sse"}]))
print("RESPONSES", post("/responses", {"model": M, "input": "PROBE-RESPONSES-RAW"}))
print("HOSTED-PROBE-DONE")`
