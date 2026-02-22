package pod

import (
	"strings"
	"testing"
)

const podWithStringChannelSurface = `
x-claw:
  pod: test-pod
services:
  svc:
    image: test:latest
    x-claw:
      agent: AGENTS.md
      surfaces:
        - "channel://discord"
`

const podWithMapChannelSurface = `
x-claw:
  pod: test-pod
services:
  svc:
    image: test:latest
    x-claw:
      agent: AGENTS.md
      surfaces:
        - channel://discord:
            dm:
              enabled: true
              policy: allowlist
              allow_from:
                - "167037070349434880"
`

const podWithMapChannelSurfaceGuilds = `
x-claw:
  pod: test-pod
services:
  svc:
    image: test:latest
    x-claw:
      agent: AGENTS.md
      surfaces:
        - channel://discord:
            guilds:
              "1465489501551067136":
                policy: allowlist
                require_mention: true
                users:
                  - "167037070349434880"
`

const podWithMixedSurfaces = `
x-claw:
  pod: test-pod
services:
  svc:
    image: test:latest
    x-claw:
      agent: AGENTS.md
      surfaces:
        - "service://trading-api"
        - channel://discord:
            dm:
              enabled: true
              policy: allowlist
              allow_from:
                - "111222333"
`

const podWithMapNonChannelSurface = `
x-claw:
  pod: test-pod
services:
  svc:
    image: test:latest
    x-claw:
      agent: AGENTS.md
      surfaces:
        - volume://cache:
            dm:
              enabled: true
`

func TestParsePodStringChannelSurface(t *testing.T) {
	p := mustParsePod(t, podWithStringChannelSurface)
	svc := p.Services["svc"]
	if len(svc.Claw.Surfaces) != 1 {
		t.Fatalf("expected 1 surface, got %d", len(svc.Claw.Surfaces))
	}
	s := svc.Claw.Surfaces[0]
	if s.Scheme != "channel" {
		t.Errorf("expected scheme=channel, got %q", s.Scheme)
	}
	if s.Target != "discord" {
		t.Errorf("expected target=discord, got %q", s.Target)
	}
	if s.ChannelConfig != nil {
		t.Error("expected ChannelConfig=nil for string-form channel surface")
	}
}

func TestParsePodMapChannelSurfaceDM(t *testing.T) {
	p := mustParsePod(t, podWithMapChannelSurface)
	svc := p.Services["svc"]
	if len(svc.Claw.Surfaces) != 1 {
		t.Fatalf("expected 1 surface, got %d", len(svc.Claw.Surfaces))
	}
	s := svc.Claw.Surfaces[0]
	if s.Scheme != "channel" || s.Target != "discord" {
		t.Errorf("expected channel://discord, got %s://%s", s.Scheme, s.Target)
	}
	if s.ChannelConfig == nil {
		t.Fatal("expected non-nil ChannelConfig")
	}
	dm := s.ChannelConfig.DM
	if !dm.Enabled {
		t.Error("expected DM.Enabled=true")
	}
	if dm.Policy != "allowlist" {
		t.Errorf("expected DM.Policy=allowlist, got %q", dm.Policy)
	}
	if len(dm.AllowFrom) != 1 || dm.AllowFrom[0] != "167037070349434880" {
		t.Errorf("expected AllowFrom=[167037070349434880], got %v", dm.AllowFrom)
	}
}

func TestParsePodMapChannelSurfaceGuilds(t *testing.T) {
	p := mustParsePod(t, podWithMapChannelSurfaceGuilds)
	svc := p.Services["svc"]
	s := svc.Claw.Surfaces[0]
	if s.ChannelConfig == nil {
		t.Fatal("expected non-nil ChannelConfig")
	}
	g, ok := s.ChannelConfig.Guilds["1465489501551067136"]
	if !ok {
		t.Fatal("expected guild 1465489501551067136")
	}
	if g.Policy != "allowlist" {
		t.Errorf("expected guild policy=allowlist, got %q", g.Policy)
	}
	if !g.RequireMention {
		t.Error("expected guild.RequireMention=true")
	}
	if len(g.Users) != 1 || g.Users[0] != "167037070349434880" {
		t.Errorf("expected guild.Users=[167037070349434880], got %v", g.Users)
	}
}

func TestParsePodMixedSurfaces(t *testing.T) {
	p := mustParsePod(t, podWithMixedSurfaces)
	svc := p.Services["svc"]
	if len(svc.Claw.Surfaces) != 2 {
		t.Fatalf("expected 2 surfaces, got %d", len(svc.Claw.Surfaces))
	}
	if svc.Claw.Surfaces[0].Scheme != "service" {
		t.Errorf("expected first surface scheme=service, got %q", svc.Claw.Surfaces[0].Scheme)
	}
	if svc.Claw.Surfaces[1].Scheme != "channel" {
		t.Errorf("expected second surface scheme=channel, got %q", svc.Claw.Surfaces[1].Scheme)
	}
	if svc.Claw.Surfaces[1].ChannelConfig == nil {
		t.Error("expected second surface ChannelConfig non-nil")
	}
}

func TestParsePodMapNonChannelSurfaceErrors(t *testing.T) {
	_, err := parsePodString(podWithMapNonChannelSurface)
	if err == nil {
		t.Fatal("expected error for map-form non-channel surface")
	}
	if !strings.Contains(err.Error(), "channel") {
		t.Errorf("expected error mentioning 'channel', got: %v", err)
	}
}

// helpers shared with other test files
func mustParsePod(t *testing.T, yaml string) *Pod {
	t.Helper()
	p, err := parsePodString(yaml)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	return p
}

func parsePodString(yaml string) (*Pod, error) {
	return Parse(strings.NewReader(yaml))
}
