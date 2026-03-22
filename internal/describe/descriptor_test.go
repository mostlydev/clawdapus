package describe

import "testing"

func TestParseDescriptorValidatesAndNormalizes(t *testing.T) {
	data := []byte(`{
	  "version": 1,
	  "description": " Trading API ",
	  "feeds": [{"name":"market-context","path":"/context","ttl":180}],
	  "endpoints": [{"method":"get","path":"/positions","description":"Open positions"}],
	  "auth": {"type":"bearer","env":"TRADING_API_TOKEN"},
	  "skill": "/app/skills/trading-api.md"
	}`)

	descriptor, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if descriptor.Description != "Trading API" {
		t.Fatalf("expected trimmed description, got %q", descriptor.Description)
	}
	if descriptor.Endpoints[0].Method != "GET" {
		t.Fatalf("expected normalized method, got %q", descriptor.Endpoints[0].Method)
	}
}

func TestBuildFeedRegistryRejectsCollisions(t *testing.T) {
	_, err := BuildFeedRegistry(map[string]*ServiceDescriptor{
		"trading-api": {
			Version: 1,
			Feeds:   []FeedDescriptor{{Name: "market-context", Path: "/context", TTL: 180}},
		},
		"pricing-api": {
			Version: 1,
			Feeds:   []FeedDescriptor{{Name: "market-context", Path: "/prices", TTL: 60}},
		},
	})
	if err == nil {
		t.Fatal("expected duplicate feed name error")
	}
}
