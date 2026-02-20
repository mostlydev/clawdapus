package clawfile

import "testing"

func TestClawConfigEmpty(t *testing.T) {
	config := NewClawConfig()

	if config.ClawType != "" {
		t.Fatalf("expected empty claw type, got %q", config.ClawType)
	}
	if len(config.Models) != 0 {
		t.Fatalf("expected no models, got %d", len(config.Models))
	}
	if len(config.Surfaces) != 0 {
		t.Fatalf("expected no surfaces, got %d", len(config.Surfaces))
	}
	if len(config.Invocations) != 0 {
		t.Fatalf("expected no invocations, got %d", len(config.Invocations))
	}
	if len(config.Privileges) != 0 {
		t.Fatalf("expected no privileges, got %d", len(config.Privileges))
	}
	if len(config.Configures) != 0 {
		t.Fatalf("expected no configures, got %d", len(config.Configures))
	}
	if len(config.Tracks) != 0 {
		t.Fatalf("expected no tracks, got %d", len(config.Tracks))
	}
}
