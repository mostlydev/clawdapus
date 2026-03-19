package pod

import (
	"strings"
	"testing"
)

const testPodWithMasterAndFeedsYAML = `
x-claw:
  pod: governance-pod
  master: octopus

services:
  octopus:
    image: claw-openclaw-example
    x-claw:
      agent: ./AGENTS.md
      feeds:
        - source: trading-api
          path: /api/v1/market-summary
          ttl: 300
        - name: fleet-alerts
          source: claw-api
          path: /fleet/alerts
          ttl: 30
  trading-api:
    image: trading-api:latest
  claw-api:
    image: claw-api:latest
`

func TestParsePodPreservesMaster(t *testing.T) {
	p, err := Parse(strings.NewReader(testPodWithMasterAndFeedsYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Master != "octopus" {
		t.Fatalf("expected master octopus, got %q", p.Master)
	}
}

func TestParsePodExtractsFeeds(t *testing.T) {
	p, err := Parse(strings.NewReader(testPodWithMasterAndFeedsYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	octopus := p.Services["octopus"]
	if octopus == nil || octopus.Claw == nil {
		t.Fatal("expected octopus claw service")
	}
	if len(octopus.Claw.Feeds) != 2 {
		t.Fatalf("expected 2 feeds, got %d", len(octopus.Claw.Feeds))
	}
	if octopus.Claw.Feeds[0].Name != "market-summary" {
		t.Fatalf("expected derived feed name market-summary, got %q", octopus.Claw.Feeds[0].Name)
	}
	if octopus.Claw.Feeds[1].Name != "fleet-alerts" {
		t.Fatalf("expected explicit feed name fleet-alerts, got %q", octopus.Claw.Feeds[1].Name)
	}
}

func TestParsePodRejectsUnknownMaster(t *testing.T) {
	const yaml = `
x-claw:
  pod: governance-pod
  master: ghost

services:
  octopus:
    image: claw-openclaw-example
    x-claw:
      agent: ./AGENTS.md
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil || !strings.Contains(err.Error(), "targets unknown service") {
		t.Fatalf("expected unknown master error, got %v", err)
	}
}

func TestParsePodRejectsMasterThatIsNotAClaw(t *testing.T) {
	const yaml = `
x-claw:
  pod: governance-pod
  master: trading-api

services:
  trading-api:
    image: trading-api:latest
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil || !strings.Contains(err.Error(), "must target a claw-managed service") {
		t.Fatalf("expected non-claw master error, got %v", err)
	}
}

func TestParsePodRejectsInvalidFeedTTL(t *testing.T) {
	const yaml = `
x-claw:
  pod: governance-pod

services:
  octopus:
    image: claw-openclaw-example
    x-claw:
      agent: ./AGENTS.md
      feeds:
        - source: trading-api
          path: /api/v1/market-summary
          ttl: 0
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil || !strings.Contains(err.Error(), "ttl must be > 0") {
		t.Fatalf("expected invalid ttl error, got %v", err)
	}
}

func TestParsePodRejectsInvalidFeedPath(t *testing.T) {
	const yaml = `
x-claw:
  pod: governance-pod

services:
  octopus:
    image: claw-openclaw-example
    x-claw:
      agent: ./AGENTS.md
      feeds:
        - source: trading-api
          path: api/v1/market-summary
          ttl: 30
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil || !strings.Contains(err.Error(), "must start with '/'") {
		t.Fatalf("expected invalid path error, got %v", err)
	}
}

func TestParsePodRejectsUnknownFeedSource(t *testing.T) {
	const yaml = `
x-claw:
  pod: governance-pod

services:
  octopus:
    image: claw-openclaw-example
    x-claw:
      agent: ./AGENTS.md
      feeds:
        - source: ghost-api
          path: /fleet/alerts
          ttl: 30
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil || !strings.Contains(err.Error(), "targets unknown source") {
		t.Fatalf("expected unknown feed source error, got %v", err)
	}
}
