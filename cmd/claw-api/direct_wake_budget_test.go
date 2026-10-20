package main

import (
	schedulepkg "github.com/mostlydev/clawdapus/internal/schedule"
	"testing"
	"time"
)

func TestCompiledTurnBudgetPreservesLongNativeTimeout(t *testing.T) {
	inv := schedulepkg.ManifestInvocation{TimeoutSeconds: 300, Wake: schedulepkg.Wake{Adapter: "openclaw-exec"}}
	if got := manifestWakeExecTimeout(inv); got != 320*time.Second {
		t.Fatalf("300-second turn shortened: %v", got)
	}
	inv.TimeoutSeconds = 0
	if got := manifestWakeExecTimeout(inv); got != 2*time.Minute {
		t.Fatalf("legacy adapter budget changed: %v", got)
	}
	inv.TimeoutSeconds = 900
	if got := manifestWakeExecTimeout(inv); got >= schedulepkg.ManualFireRequestTimeout {
		t.Fatalf("outer request cannot cover wake: %v", got)
	}
}

func TestSchedulerRejectsUnboundedCompiledTurn(t *testing.T) {
	for _, seconds := range []int{-1, 901} {
		_, err := newScheduler(&schedulepkg.Manifest{Invocations: []schedulepkg.ManifestInvocation{{ID: "invalid", TimeoutSeconds: seconds, Schedule: "* * * * *"}}}, nil, nil, nil)
		if err == nil {
			t.Fatalf("invalid timeout accepted: %d", seconds)
		}
	}
}
