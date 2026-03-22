package pod

import (
	"strings"
	"testing"
)

func TestParsePodAcceptsShortFormFeeds(t *testing.T) {
	const yaml = `
x-claw:
  pod: governance-pod

services:
  trading-api:
    image: trading-api:latest
  octopus:
    image: claw-openclaw-example
    x-claw:
      agent: ./AGENTS.md
      feeds:
        - market-context
        - name: fleet-alerts
          source: trading-api
          path: /fleet/alerts
          ttl: 30
`

	p, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	feeds := p.Services["octopus"].Claw.Feeds
	if len(feeds) != 2 {
		t.Fatalf("expected 2 feeds, got %+v", feeds)
	}
	if !feeds[0].Unresolved || feeds[0].Name != "market-context" {
		t.Fatalf("expected unresolved short-form feed, got %+v", feeds[0])
	}
	if feeds[1].Unresolved {
		t.Fatalf("expected explicit feed to remain resolved, got %+v", feeds[1])
	}
}

func TestParsePodDoesNotValidateSourceForUnresolvedFeeds(t *testing.T) {
	const yaml = `
x-claw:
  pod: governance-pod

services:
  octopus:
    image: claw-openclaw-example
    x-claw:
      agent: ./AGENTS.md
      feeds:
        - fleet-alerts
`

	if _, err := Parse(strings.NewReader(yaml)); err != nil {
		t.Fatalf("expected unresolved feed names to survive parse, got %v", err)
	}
}
