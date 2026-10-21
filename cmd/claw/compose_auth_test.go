package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/cllama"
	"github.com/mostlydev/clawdapus/internal/describe"
	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/driver/picoclaw"
	"github.com/mostlydev/clawdapus/internal/pod"
	"github.com/mostlydev/clawdapus/internal/runs"
)

func perRunAuthFixture(t *testing.T) (*pod.Pod, map[string]*describe.ServiceDescriptor, []describe.ToolSpec) {
	t.Helper()
	p, err := pod.Parse(strings.NewReader(`
x-claw:
  cllama-defaults:
    proxy: [passthrough]
    env:
      SERVICE_TOKEN: ${SHARED_INGRESS}
services:
  worker:
    image: worker:test
    x-claw:
      agent: ./AGENTS.md
      lifecycle: per-run
      tools: [reader]
      feeds:
        - name: updates
          source: reader
          path: /feed
          ttl: 30
  reader:
    image: reader:test
    expose: ["8080"]
    environment:
      SERVICE_TOKEN: ${SHARED_INGRESS}
`))
	if err != nil {
		t.Fatal(err)
	}
	descriptors := map[string]*describe.ServiceDescriptor{"reader": {
		Version: 2, Auth: &describe.AuthDescriptor{Type: "bearer", Env: "SERVICE_TOKEN"},
	}}
	tools := []describe.ToolSpec{{Name: "read", Service: "reader", InputSchema: map[string]interface{}{"type": "object"}, HTTP: &describe.ToolHTTP{Method: "POST", Path: "/r/{claw_id}/read"}}}
	return p, descriptors, tools
}

func TestPerRunProxyCredentialCustodyAndRotation(t *testing.T) {
	dir := t.TempDir()
	agent := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(agent, []byte("Read only the task's permitted resources."), 0o600); err != nil {
		t.Fatal(err)
	}
	p, descriptors, specs := perRunAuthFixture(t)
	worker, err := (&picoclaw.Driver{}).CompileRunWorker(&driver.ResolvedClaw{
		ServiceName: "worker", ClawType: "picoclaw", AgentHostPath: agent,
		Cllama: []string{"passthrough"}, Models: map[string]string{"primary": "openai/gpt-4o"},
		Environment: p.Services["worker"].Environment,
	}, driver.RunWorkerOpts{PodName: "auth-test"})
	if err != nil {
		t.Fatal(err)
	}
	compile := func(token, endpoint, toolName string, rounds int) string {
		t.Helper()
		env := map[string]string{"SHARED_INGRESS": token}
		tools, err := buildToolManifestEntries(p, descriptors, env, "worker", specs, nil)
		if err != nil {
			t.Fatal(err)
		}
		tools[0].Execution.BaseURL = endpoint
		tools[0].Name = toolName
		feeds, err := buildFeedManifestEntries(p, descriptors, env, "worker", "worker", nil)
		if err != nil {
			t.Fatal(err)
		}
		policy := cllama.DefaultToolPolicy
		policy.MaxRounds = rounds
		tpl := &runs.Template{Name: "worker", Worker: *worker, ToolsGrant: []string{toolName}}
		contexts := map[string]*runs.ContextTemplate{"worker": {Input: cllama.AgentContextInput{Tools: tools, Feeds: feeds, ToolPolicy: &policy}}}
		if err := runs.WriteTemplates(dir, []*runs.Template{tpl}, contexts); err != nil {
			t.Fatal(err)
		}
		public, err := os.ReadFile(filepath.Join(runs.TemplateDir(dir), "worker.json"))
		if err != nil {
			t.Fatal(err)
		}
		workerJSON, _ := json.Marshal(worker)
		if strings.Contains(string(public), token) || strings.Contains(string(workerJSON), token) {
			t.Fatal("proxy credential escaped into public template or worker artifact")
		}
		privatePath := filepath.Join(runs.TemplateDir(dir), "worker.context.json")
		private, err := os.ReadFile(privatePath)
		if err != nil || !strings.Contains(string(private), token) {
			t.Fatal("proxy context did not retain its credential")
		}
		info, err := os.Stat(privatePath)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal("credential-bearing template context is not private")
		}
		return tpl.ID
	}
	first := compile("fixture-ingress", "http://reader:8080", "reader.read", 8)
	rotated := compile("rotated-ingress", "http://reader:8080", "reader.read", 8)
	if first != rotated {
		t.Fatal("credential-only rotation changed template identity")
	}
	for _, changed := range []string{
		compile("rotated-ingress", "http://other:8080", "reader.read", 8),
		compile("rotated-ingress", "http://reader:8080", "reader.other", 8),
		compile("rotated-ingress", "http://reader:8080", "reader.read", 9),
	} {
		if changed == rotated {
			t.Fatal("execution or policy change did not change template identity")
		}
	}
}

func TestPerRunManifestAuthUsesProxyOnlyDefaults(t *testing.T) {
	p, descriptors, specs := perRunAuthFixture(t)
	env := map[string]string{"SHARED_INGRESS": "fixture-ingress"}
	tools, err := buildToolManifestEntries(p, descriptors, env, "worker", specs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tools[0].Execution.Auth == nil || tools[0].Execution.Auth.Token != env["SHARED_INGRESS"] {
		t.Fatal("tool bearer was not resolved from proxy-only defaults")
	}
	feeds, err := buildFeedManifestEntries(p, descriptors, env, "worker", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	if feeds[0].Auth != env["SHARED_INGRESS"] || len(p.Services["worker"].Environment) != 0 {
		t.Fatal("feed bearer resolution or worker credential isolation failed")
	}
}

func TestPerRunManifestAuthMissingFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		proxy map[string]string
		env   map[string]string
	}{
		{"undeclared", nil, map[string]string{"SERVICE_TOKEN": "host-must-not-be-used"}},
		{"empty", map[string]string{"SERVICE_TOKEN": ""}, nil},
		{"blank", map[string]string{"SERVICE_TOKEN": "   "}, nil},
		{"unresolved", map[string]string{"SERVICE_TOKEN": "${MISSING}"}, nil},
		{"expanded blank", map[string]string{"SERVICE_TOKEN": "${BLANK}"}, map[string]string{"BLANK": "   "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, descriptors, specs := perRunAuthFixture(t)
			p.Services["worker"].Claw.CllamaEnv = tc.proxy
			if _, err := buildToolManifestEntries(p, descriptors, tc.env, "worker", specs, nil); err == nil {
				t.Fatal("missing proxy-only tool auth was accepted")
			}
			if _, err := buildFeedManifestEntries(p, descriptors, tc.env, "worker", "worker", nil); err == nil {
				t.Fatal("missing proxy-only feed auth was accepted")
			}
		})
	}
}

func TestManifestAuthPreservesLegacyAndProjectedPrecedence(t *testing.T) {
	p, descriptors, specs := perRunAuthFixture(t)
	p.Services["worker"].Claw.Lifecycle = ""
	p.Services["worker"].Environment = map[string]string{"SERVICE_TOKEN": "legacy-ingress"}
	p.Services["worker"].Claw.CllamaEnv = map[string]string{"SERVICE_TOKEN": "proxy-ingress"}
	tools, err := buildToolManifestEntries(p, descriptors, nil, "worker", specs, nil)
	if err != nil || tools[0].Execution.Auth.Token != "legacy-ingress" {
		t.Fatalf("long-lived credential precedence changed: %v", err)
	}
	p.Services["worker"].Claw.Lifecycle = pod.LifecyclePerRun
	p.Services["worker"].Claw.CllamaEnv = nil
	projected := []cllama.ServiceAuthEntry{{Service: "reader", AuthType: "bearer", Token: "projected-ingress"}}
	tools, err = buildToolManifestEntries(p, descriptors, nil, "worker", specs, projected)
	if err != nil || tools[0].Execution.Auth.Token != "projected-ingress" {
		t.Fatalf("projected credential precedence changed: %v", err)
	}
}
