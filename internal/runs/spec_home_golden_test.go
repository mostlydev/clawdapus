package runs

import (
	"encoding/json"
	"testing"
)

// TestCanonicalSpecGoldenHome pins the canonical bytes and digest of launch
// specs that carry a task home. Orchestrators pin the same vectors.
func TestCanonicalSpecGoldenHome(t *testing.T) {
	for _, tc := range []struct{ in, want, digest string }{
		{
			`{"template":"worker-home","task":"hello","tools":[],"max_runtime_seconds":900,"home":"a42"}`,
			`{"home":"a42","max_runtime_seconds":900,"task":"hello","template":"worker-home","tools":[]}`,
			"sha256:6c273dc7f80b34b70734a808227de353c0a4225c17d9d840596c83fc985c17ac",
		},
		{
			`{"home":"a42","max_runtime_seconds":60,"task":"Hello ✓","template":"hermes-worker","tools":[]}`,
			`{"home":"a42","max_runtime_seconds":60,"task":"Hello ✓","template":"hermes-worker","tools":[]}`,
			"sha256:06dfa9a9308ee9cc9bc979efd9c04728fa7caf5190e71fd547543cbffa3d72e3",
		},
	} {
		got, spec, digest, err := CanonicalSpec(json.RawMessage(tc.in))
		if err != nil {
			t.Fatalf("%s: %v", tc.in, err)
		}
		if string(got) != tc.want || digest != tc.digest || spec.Home != "a42" {
			t.Errorf("canonical(%s)\n got %s %s\nwant %s %s", tc.in, got, digest, tc.want, tc.digest)
		}
	}
}
