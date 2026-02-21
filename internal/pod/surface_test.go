package pod

import (
	"testing"
)

func TestParseSurfaceVolumeReadWrite(t *testing.T) {
	s, err := ParseSurface("volume://research-cache read-write")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Scheme != "volume" {
		t.Errorf("expected scheme=volume, got %q", s.Scheme)
	}
	if s.Target != "research-cache" {
		t.Errorf("expected target=research-cache, got %q", s.Target)
	}
	if s.AccessMode != "read-write" {
		t.Errorf("expected access=read-write, got %q", s.AccessMode)
	}
}

func TestParseSurfaceVolumeReadOnly(t *testing.T) {
	s, err := ParseSurface("volume://research-cache read-only")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.AccessMode != "read-only" {
		t.Errorf("expected access=read-only, got %q", s.AccessMode)
	}
}

func TestParseSurfaceChannelNoAccess(t *testing.T) {
	s, err := ParseSurface("channel://discord")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Scheme != "channel" {
		t.Errorf("expected scheme=channel, got %q", s.Scheme)
	}
	if s.Target != "discord" {
		t.Errorf("expected target=discord, got %q", s.Target)
	}
	if s.AccessMode != "" {
		t.Errorf("expected empty access mode, got %q", s.AccessMode)
	}
}

func TestParseSurfaceOpaqueURI(t *testing.T) {
	s, err := ParseSurface("volume:shared-cache read-write")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Target != "shared-cache" {
		t.Errorf("expected target=shared-cache, got %q", s.Target)
	}
}

func TestParseSurfaceEmptyReturnsError(t *testing.T) {
	_, err := ParseSurface("")
	if err == nil {
		t.Fatal("expected error for empty surface")
	}
}

func TestParseSurfaceNoSchemeReturnsError(t *testing.T) {
	_, err := ParseSurface("just-a-name")
	if err == nil {
		t.Fatal("expected error for surface without scheme")
	}
}
