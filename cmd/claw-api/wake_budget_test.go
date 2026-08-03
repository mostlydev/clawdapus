package main

import (
	"testing"
	"time"

	schedulepkg "github.com/mostlydev/clawdapus/internal/schedule"
)

// knownWakeAdapters mirrors every adapter resolveWakeAdapter can emit in
// cmd/claw/schedule_manifest.go. Adding an adapter there without adding it here
// leaves its budget outside this invariant.
var knownWakeAdapters = []string{
	"openclaw-exec",
	"hermes-exec",
	"nanobot-exec",
	"picoclaw-exec",
	"nullclaw-exec",
}

// schedulepkg.MaxWakeExecTimeout is what the synchronous operator client sizes
// its request and transport budgets against. If any adapter is allowed to run
// longer than it, `claw api schedule fire` goes back to being cut short by its
// own client while the server keeps waiting -- the exact failure #348 fixed.
// Nothing but this test holds the constant to its name.
func TestEveryWakeAdapterFitsMaxWakeExecTimeout(t *testing.T) {
	for _, adapter := range knownWakeAdapters {
		if got := wakeExecTimeout(adapter); got > schedulepkg.MaxWakeExecTimeout {
			t.Errorf("adapter %s wake budget %v exceeds MaxWakeExecTimeout %v; the manual fire client cannot cover it",
				adapter, got, schedulepkg.MaxWakeExecTimeout)
		}
	}
}

// The margins exist so the outer transport outlives the inner request, which in
// turn outlives the longest wake. Assert the ordering rather than the literals,
// so tuning a margin cannot silently invert it.
func TestManualFireBudgetsAreStrictlyNested(t *testing.T) {
	if schedulepkg.ManualFireRequestTimeout <= schedulepkg.MaxWakeExecTimeout {
		t.Fatalf("manual fire request budget %v must exceed the longest wake %v",
			schedulepkg.ManualFireRequestTimeout, schedulepkg.MaxWakeExecTimeout)
	}
	if schedulepkg.ManualFireTransportTimeout <= schedulepkg.ManualFireRequestTimeout {
		t.Fatalf("manual fire transport budget %v must exceed the request budget %v",
			schedulepkg.ManualFireTransportTimeout, schedulepkg.ManualFireRequestTimeout)
	}
}

// Adapters with no evidence that they need more keep the generic budget.
func TestUnknownAdapterKeepsDefaultWakeBudget(t *testing.T) {
	if got := wakeExecTimeout("some-future-exec"); got != defaultWakeExecTimeout {
		t.Fatalf("expected default wake timeout %v, got %v", defaultWakeExecTimeout, got)
	}
	if defaultWakeExecTimeout > schedulepkg.MaxWakeExecTimeout {
		t.Fatalf("default wake budget %v exceeds max %v", defaultWakeExecTimeout, schedulepkg.MaxWakeExecTimeout)
	}
	var _ time.Duration = defaultWakeExecTimeout
}
