package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/pod"
)

// TestExamplePodsParseAndAgentPathsExist walks every example pod file and
// asserts it parses and that each service's agent contract file exists.
// Examples are the first thing evaluators touch; this is the cheap guard
// against YAML and agent-path rot that the credentialed spike tests cannot
// provide unconditionally.
func TestExamplePodsParseAndAgentPathsExist(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	repoRoot, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(repoRoot, "examples", "*", "claw-pod.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no example pod files found — glob or layout changed")
	}
	for _, podPath := range matches {
		exampleDir := filepath.Dir(podPath)
		t.Run(filepath.Base(exampleDir), func(t *testing.T) {
			f, err := os.Open(podPath)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			p, err := pod.Parse(f)
			if err != nil {
				t.Fatalf("pod parse: %v", err)
			}
			for name, svc := range p.Services {
				if svc == nil || svc.Claw == nil {
					continue
				}
				agent := strings.TrimSpace(svc.Claw.Agent)
				if agent == "" || strings.Contains(agent, "${") {
					continue
				}
				agentPath := agent
				if !filepath.IsAbs(agentPath) {
					agentPath = filepath.Join(exampleDir, agent)
				}
				if _, err := os.Stat(agentPath); err != nil {
					t.Errorf("service %q agent contract: %v", name, err)
				}
			}
		})
	}
}
