package hermes

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestBaseImageTag(t *testing.T) {
	d := &Driver{}

	if _, ok := interface{}(d).(driver.BaseImageProvider); ok {
		t.Fatal("hermes-base is a published pinned image; do not keep a second inline auto-build recipe")
	}
	if !strings.HasPrefix(BaseImageVersion, UpstreamTag+"-claw.") {
		t.Fatalf("expected patched Hermes image tag to derive from upstream tag %q, got %q", UpstreamTag, BaseImageVersion)
	}
	if BaseImageTag != "ghcr.io/mostlydev/hermes-base:"+BaseImageVersion {
		t.Fatalf("unexpected Hermes base image tag: %s", BaseImageTag)
	}
}
