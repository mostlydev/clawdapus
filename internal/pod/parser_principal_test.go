package pod

import (
	"strings"
	"testing"
)

func TestParseClawAPIModeSelf(t *testing.T) {
	const yaml = `
x-claw:
  pod: test-pod

services:
  analyst:
    image: analyst:latest
    x-claw:
      agent: ./AGENTS.md
      claw-api: self
`
	p, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	svc := p.Services["analyst"]
	if svc == nil || svc.Claw == nil {
		t.Fatal("expected analyst claw service")
	}
	if svc.Claw.ClawAPIMode != "self" {
		t.Fatalf("expected ClawAPIMode \"self\", got %q", svc.Claw.ClawAPIMode)
	}
}

func TestParseClawAPIModeEmpty(t *testing.T) {
	const yaml = `
x-claw:
  pod: test-pod

services:
  analyst:
    image: analyst:latest
    x-claw:
      agent: ./AGENTS.md
`
	p, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	svc := p.Services["analyst"]
	if svc == nil || svc.Claw == nil {
		t.Fatal("expected analyst claw service")
	}
	if svc.Claw.ClawAPIMode != "" {
		t.Fatalf("expected empty ClawAPIMode, got %q", svc.Claw.ClawAPIMode)
	}
}

func TestParseClawAPIModeUnknownValueRejected(t *testing.T) {
	const yaml = `
x-claw:
  pod: test-pod

services:
  analyst:
    image: analyst:latest
    x-claw:
      agent: ./AGENTS.md
      claw-api: write
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected parse error for unknown claw-api value")
	}
	if !strings.Contains(err.Error(), "unsupported value") {
		t.Fatalf("expected \"unsupported value\" in error, got %v", err)
	}
}

func TestParsePrincipalsBasicDeclaration(t *testing.T) {
	const yaml = `
x-claw:
  pod: test-pod
  principals:
    - name: dashboard
      verbs: [fleet.status]
      scope: pod

services:
  worker:
    image: worker:latest
    x-claw:
      agent: ./AGENTS.md
`
	p, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.Principals) != 1 {
		t.Fatalf("expected 1 principal, got %d", len(p.Principals))
	}
	pp := p.Principals[0]
	if pp.Name != "dashboard" {
		t.Fatalf("expected principal name \"dashboard\", got %q", pp.Name)
	}
	if len(pp.Verbs) != 1 || pp.Verbs[0] != "fleet.status" {
		t.Fatalf("expected verbs [fleet.status], got %v", pp.Verbs)
	}
	if pp.Scope != "pod" {
		t.Fatalf("expected scope \"pod\", got %q", pp.Scope)
	}
}

func TestParsePrincipalsRejectsEmptyName(t *testing.T) {
	const yaml = `
x-claw:
  pod: test-pod
  principals:
    - name: ""
      verbs: [fleet.status]

services:
  worker:
    image: worker:latest
    x-claw:
      agent: ./AGENTS.md
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected parse error for empty principal name")
	}
}

func TestParsePrincipalsRejectsEmptyVerbs(t *testing.T) {
	const yaml = `
x-claw:
  pod: test-pod
  principals:
    - name: dashboard
      verbs: []

services:
  worker:
    image: worker:latest
    x-claw:
      agent: ./AGENTS.md
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected parse error for empty verbs")
	}
}

func TestParsePrincipalsRejectsUnknownVerb(t *testing.T) {
	const yaml = `
x-claw:
  pod: test-pod
  principals:
    - name: dashboard
      verbs: [fleet.explode]

services:
  worker:
    image: worker:latest
    x-claw:
      agent: ./AGENTS.md
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected parse error for unknown verb")
	}
	if !strings.Contains(err.Error(), "unknown verb") {
		t.Fatalf("expected \"unknown verb\" in error, got %v", err)
	}
}

func TestParsePrincipalsScopeAndServicesMutuallyExclusive(t *testing.T) {
	const yaml = `
x-claw:
  pod: test-pod
  principals:
    - name: dashboard
      verbs: [fleet.status]
      scope: pod
      services: [worker]

services:
  worker:
    image: worker:latest
    x-claw:
      agent: ./AGENTS.md
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected parse error for scope: pod with services")
	}
}

func TestParsePrincipalsInjectIntoMustExist(t *testing.T) {
	const yaml = `
x-claw:
  pod: test-pod
  principals:
    - name: dashboard
      verbs: [fleet.status]
      inject-into: nonexistent-service

services:
  worker:
    image: worker:latest
    x-claw:
      agent: ./AGENTS.md
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected parse error for nonexistent inject-into service")
	}
}

func TestParsePrincipalsInjectIntoValid(t *testing.T) {
	const yaml = `
x-claw:
  pod: test-pod
  principals:
    - name: ci-pipeline
      verbs: [fleet.status, fleet.logs]
      services: [worker]
      inject-into: worker

services:
  worker:
    image: worker:latest
    x-claw:
      agent: ./AGENTS.md
`
	p, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.Principals) != 1 {
		t.Fatalf("expected 1 principal, got %d", len(p.Principals))
	}
	pp := p.Principals[0]
	if pp.InjectInto != "worker" {
		t.Fatalf("expected inject-into \"worker\", got %q", pp.InjectInto)
	}
}

func TestParsePrincipalsNilWhenAbsent(t *testing.T) {
	const yaml = `
x-claw:
  pod: test-pod

services:
  worker:
    image: worker:latest
    x-claw:
      agent: ./AGENTS.md
`
	p, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Principals != nil {
		t.Fatalf("expected nil Principals, got %v", p.Principals)
	}
}
