package pod

import (
	"os"
	"path/filepath"
	"testing"
)

// repoRoot walks up from the current file's directory until it finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (no go.mod found)")
		}
		dir = parent
	}
}

func TestMasterClawExampleParsesCorrectly(t *testing.T) {
	root := repoRoot(t)
	podPath := filepath.Join(root, "examples", "master-claw", "claw-pod.yml")

	f, err := os.Open(podPath)
	if err != nil {
		t.Fatalf("open pod file: %v", err)
	}
	defer f.Close()

	p, err := Parse(f)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Pod name
	if p.Name != "master-claw-demo" {
		t.Errorf("expected pod name %q, got %q", "master-claw-demo", p.Name)
	}

	// Master designation
	if p.Master != "governor" {
		t.Errorf("expected master %q, got %q", "governor", p.Master)
	}

	// Exactly 3 services
	if got := len(p.Services); got != 3 {
		t.Fatalf("expected 3 services, got %d", got)
	}

	// All three service names exist
	for _, name := range []string{"worker-a", "worker-b", "governor"} {
		if _, ok := p.Services[name]; !ok {
			t.Errorf("missing expected service %q", name)
		}
	}

	// All services are claw-managed and have cllama enabled
	for name, svc := range p.Services {
		if svc.Claw == nil {
			t.Errorf("service %q: expected claw block, got nil", name)
			continue
		}
		if len(svc.Claw.Cllama) == 0 {
			t.Errorf("service %q: expected cllama to be set", name)
		}
		if len(svc.Claw.CllamaEnv) == 0 {
			t.Errorf("service %q: expected cllama-env to be set", name)
		}
	}

	// Governor-specific assertions
	gov := p.Services["governor"]
	if gov.Claw.Agent != "./agents/GOVERNOR.md" {
		t.Errorf("governor agent: got %q", gov.Claw.Agent)
	}

	// Governor has feeds
	if len(gov.Claw.Feeds) == 0 {
		t.Fatal("governor: expected at least one feed entry")
	}
	feed := gov.Claw.Feeds[0]
	if feed.Source != "claw-api" {
		t.Errorf("governor feed source: expected %q, got %q", "claw-api", feed.Source)
	}
	if feed.Path != "/fleet/alerts" {
		t.Errorf("governor feed path: expected %q, got %q", "/fleet/alerts", feed.Path)
	}
	if feed.TTL != 30 {
		t.Errorf("governor feed TTL: expected 30, got %d", feed.TTL)
	}

	// Governor has invoke entries
	if len(gov.Claw.Invoke) == 0 {
		t.Fatal("governor: expected at least one invoke entry")
	}
	inv := gov.Claw.Invoke[0]
	if inv.Schedule != "*/5 * * * *" {
		t.Errorf("governor invoke schedule: got %q", inv.Schedule)
	}

	// Governor has service surface
	if len(gov.Claw.Surfaces) == 0 {
		t.Error("governor: expected at least one surface")
	}

	// Workers have correct agent refs
	for _, name := range []string{"worker-a", "worker-b"} {
		svc := p.Services[name]
		if svc.Claw.Agent != "./agents/WORKER.md" {
			t.Errorf("%s agent: expected %q, got %q", name, "./agents/WORKER.md", svc.Claw.Agent)
		}
	}
}
