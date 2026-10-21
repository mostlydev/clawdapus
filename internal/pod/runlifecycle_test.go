package pod

import (
	"strings"
	"testing"
	"time"

	"github.com/mostlydev/clawdapus/internal/driver"
	"gopkg.in/yaml.v3"
)

func parsePerRunPod(t *testing.T, xclaw string, extra string) (*Pod, error) {
	t.Helper()
	yaml := `
x-claw:
  pod: runs
services:
  worker:
    image: picoclaw-worker:latest
` + extra + `
    x-claw:
      agent: ./AGENTS.md
      cllama: passthrough
` + xclaw
	return Parse(strings.NewReader(yaml))
}

func TestParsePerRunLifecycleDefaults(t *testing.T) {
	p, err := parsePerRunPod(t, "      lifecycle: per-run\n", "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	svc := p.Services["worker"]
	if !svc.IsPerRun() || !svc.IsAgentManaged() {
		t.Fatalf("expected an agent-managed per-run service")
	}
	if svc.Claw.Run == nil || svc.Claw.Run.MaxRuntime != DefaultRunMaxRuntime || svc.Claw.Run.MaxAuthority != DefaultRunMaxAuthority ||
		svc.Claw.Run.MaxTaskBytes != DefaultRunMaxTaskBytes || svc.Claw.Run.MaxResultBytes != DefaultRunMaxResultBytes {
		t.Fatalf("unexpected default limits %+v", svc.Claw.Run)
	}
}

func TestParsePerRunLimits(t *testing.T) {
	p, err := parsePerRunPod(t, `      lifecycle: per-run
      run:
        max-runtime: 10m
        max-authority: 1h
        max-task-bytes: 4096
        max-result-bytes: 8192
        memory: 512m
        pids: 64
`, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := p.Services["worker"].Claw.Run
	want := RunLimits{MaxRuntime: 10 * time.Minute, MaxAuthority: time.Hour, MaxTaskBytes: 4096, MaxResultBytes: 8192, Memory: "512m", Pids: 64}
	if *got != want {
		t.Fatalf("limits = %+v, want %+v", *got, want)
	}
}

func TestParseServiceLifecycleIsDefault(t *testing.T) {
	p, err := parsePerRunPod(t, "      lifecycle: service\n", "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.Services["worker"].IsPerRun() || p.Services["worker"].Claw.Run != nil {
		t.Fatalf("lifecycle: service must stay long-lived")
	}
}

func TestParsePerRunRejections(t *testing.T) {
	cases := []struct {
		name, xclaw, extra, want string
	}{
		{"unknown lifecycle", "      lifecycle: sometimes\n", "", "unknown value"},
		{"run without per-run", "      run:\n        max-runtime: 1m\n", "", "requires x-claw.lifecycle"},
		{"bad duration", "      lifecycle: per-run\n      run:\n        max-runtime: soon\n", "", "whole number of seconds"},
		{"task ceiling", "      lifecycle: per-run\n      run:\n        max-task-bytes: 999999999\n", "", "max-task-bytes"},
		{"count", "      lifecycle: per-run\n      count: 2\n", "", "count"},
		{"handles", "      lifecycle: per-run\n      handles:\n        discord:\n          id: \"1\"\n", "", "handles"},
		{"invoke", "      lifecycle: per-run\n      invoke:\n        - schedule: \"0 * * * *\"\n          message: hi\n", "", "invoke"},
		{"surfaces", "      lifecycle: per-run\n      surfaces:\n        - service://api\n", "", "surfaces"},
		{"skills", "      lifecycle: per-run\n      skills:\n        - ./x.md\n", "", "skills"},
		{"persona", "      lifecycle: per-run\n      persona: ./persona\n", "", "persona"},
		{"claw-api", "      lifecycle: per-run\n      claw-api: self\n", "", "claw-api"},
		{"volumes", "      lifecycle: per-run\n", "    volumes:\n      - ./data:/data\n", `"volumes"`},
		{"privileged", "      lifecycle: per-run\n", "    privileged: true\n", `"privileged"`},
		{"environment", "      lifecycle: per-run\n", "    environment:\n      A: b\n", `"environment"`},
		{"command", "      lifecycle: per-run\n", "    command: [x]\n", `"command"`},
		{"sub-second runtime", "      lifecycle: per-run\n      run:\n        max-runtime: 1ns\n", "", "whole number of seconds"},
		{"fractional authority", "      lifecycle: per-run\n      run:\n        max-authority: 1500ms\n", "", "whole number of seconds"},
		{"memory syntax", "      lifecycle: per-run\n      run:\n        memory: lots\n", "", "Docker memory size"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parsePerRunPod(t, tc.xclaw, tc.extra)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestParseMasterCannotTargetPerRun(t *testing.T) {
	_, err := Parse(strings.NewReader(`
x-claw:
  pod: runs
  master: worker
services:
  worker:
    image: picoclaw-worker:latest
    x-claw:
      agent: ./AGENTS.md
      lifecycle: per-run
`))
	if err == nil || !strings.Contains(err.Error(), "per-run") {
		t.Fatalf("expected master/per-run rejection, got %v", err)
	}
}

func TestEmitComposeSkipsPerRunAndAddsInternalRunNetwork(t *testing.T) {
	p := &Pod{
		Name:       "test-pod",
		RunNetwork: "claw-runs-abc123",
		Services: map[string]*Service{
			"bot":    {Image: "bot:latest", Claw: &ClawBlock{Count: 1}},
			"worker": {Image: "worker:latest", Claw: &ClawBlock{Lifecycle: LifecyclePerRun, Run: &RunLimits{}}},
			"api":    {Image: "api:latest"},
		},
	}
	results := map[string]*driver.MaterializeResult{"bot": {ReadOnly: true, Restart: "on-failure"}}
	proxies := []CllamaProxyConfig{{
		ProxyType:         "passthrough",
		Image:             "ghcr.io/mostlydev/cllama:latest",
		ContextHostDir:    "/tmp/test/.claw-runtime/context",
		AuthHostDir:       "/tmp/test/.claw-auth",
		RevocationHostDir: "/tmp/test/.claw-revocations",
		PodName:           "test-pod",
	}}
	out, err := EmitCompose(p, results, proxies...)
	if err != nil {
		t.Fatal(err)
	}
	var cf struct {
		Services map[string]struct {
			Networks    []string          `yaml:"networks"`
			Volumes     []string          `yaml:"volumes"`
			Environment map[string]string `yaml:"environment"`
		} `yaml:"services"`
		Networks map[string]struct {
			Name     string `yaml:"name"`
			Internal bool   `yaml:"internal"`
		} `yaml:"networks"`
	}
	if err := yaml.Unmarshal([]byte(out), &cf); err != nil {
		t.Fatal(err)
	}
	if _, ok := cf.Services["worker"]; ok {
		t.Fatalf("per-run template must not be emitted as a long-lived compose service")
	}
	runNet, ok := cf.Networks[RunNetworkKey]
	if !ok || runNet.Name != "claw-runs-abc123" || !runNet.Internal {
		t.Fatalf("expected internal named run network, got %+v (ok=%v)", runNet, ok)
	}
	for name, svc := range cf.Services {
		onRunNet := false
		for _, n := range svc.Networks {
			if n == RunNetworkKey {
				onRunNet = true
			}
		}
		if onRunNet != (name == "cllama") {
			t.Fatalf("service %q on run network = %v; only cllama may share it with workers", name, onRunNet)
		}
	}
	proxy := cf.Services["cllama"]
	if proxy.Environment["CLAW_REVOCATION_DIR"] != "/claw/revocations" {
		t.Fatalf("expected CLAW_REVOCATION_DIR, got %v", proxy.Environment)
	}
	if !strings.Contains(strings.Join(proxy.Volumes, "\n"), "/tmp/test/.claw-revocations:/claw/revocations:rw") {
		t.Fatalf("expected rw revocation mount, got %v", proxy.Volumes)
	}
}

func TestEmitComposeWithoutRunNetworkLeavesProxyAlone(t *testing.T) {
	p := &Pod{Name: "test-pod", Services: map[string]*Service{"bot": {Image: "bot:latest", Claw: &ClawBlock{Count: 1}}}}
	out, err := EmitCompose(p, map[string]*driver.MaterializeResult{"bot": {}}, CllamaProxyConfig{
		ProxyType: "passthrough", Image: "cllama:x", ContextHostDir: "/c", AuthHostDir: "/a", PodName: "test-pod",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, RunNetworkKey) || strings.Contains(out, "CLAW_REVOCATION_DIR") {
		t.Fatalf("no per-run artifacts expected:\n%s", out)
	}
}
