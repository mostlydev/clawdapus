package infraimages

import "testing"

func TestReleaseRefs(t *testing.T) {
	got := ReleaseRefs("v1.2.3")
	want := []string{
		"ghcr.io/mostlydev/claw-api:v1.2.3",
		"ghcr.io/mostlydev/clawdash:v1.2.3",
		"ghcr.io/mostlydev/claw-wall:v1.2.3",
		"ghcr.io/mostlydev/claw-mcp-stdio:v1.2.3",
		"ghcr.io/mostlydev/cllama:" + DefaultCllamaTag,
		"ghcr.io/mostlydev/hermes-base:" + DefaultHermesBaseTag,
	}

	if len(got) != len(want) {
		t.Fatalf("ReleaseRefs length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ReleaseRefs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
