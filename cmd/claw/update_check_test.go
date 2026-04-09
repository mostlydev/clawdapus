package main

import "testing"

func TestIsNewerRelease(t *testing.T) {
	tests := []struct {
		name    string
		latest  string
		current string
		want    bool
	}{
		{"strictly newer patch", "0.8.2", "0.8.1", true},
		{"strictly newer minor", "0.9.0", "0.8.9", true},
		{"strictly newer major", "1.0.0", "0.99.9", true},
		{"equal", "0.8.2", "0.8.2", false},

		// Regression: v0.8.1 cache read by a freshly upgraded v0.8.2 binary
		// was printing "Update available: v0.8.2 → v0.8.1" because the pre-fix
		// code compared with != instead of semver ordering.
		{"stale cache after upgrade", "0.8.1", "0.8.2", false},

		{"empty latest", "", "0.8.2", false},
		{"empty current", "0.8.2", "", false},
		{"both empty", "", "", false},
		{"malformed latest", "garbage", "0.8.2", false},
		{"malformed current", "0.8.2", "garbage", false},

		// Accept strings with or without a leading "v" on either side —
		// the cache historically stores unprefixed values but the field could
		// drift; the comparator should still work.
		{"v-prefixed latest", "v0.8.2", "0.8.1", true},
		{"v-prefixed current", "0.8.2", "v0.8.1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNewerRelease(tt.latest, tt.current)
			if got != tt.want {
				t.Errorf("isNewerRelease(%q, %q) = %v, want %v", tt.latest, tt.current, got, tt.want)
			}
		})
	}
}
