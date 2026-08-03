package driver

import (
	"strings"
	"testing"
)

// Retired runners must fail closed with guidance, not fall through to the
// generic "no registered driver" message. See ADR-026.
func TestLookupRejectsRetiredRunnersWithMigrationGuidance(t *testing.T) {
	for _, name := range []string{"nanoclaw", "microclaw", "nullclaw"} {
		t.Run(name, func(t *testing.T) {
			d, err := Lookup(name)
			if err == nil {
				t.Fatalf("expected %s to be rejected, got driver %T", name, d)
			}
			msg := err.Error()
			if !strings.Contains(msg, name) {
				t.Errorf("error should name the retired runner, got %q", msg)
			}
			if !strings.Contains(msg, "retired") {
				t.Errorf("error should say the runner was retired, got %q", msg)
			}
			if !strings.Contains(msg, "ADR-026") {
				t.Errorf("error should point at the decision record, got %q", msg)
			}
			for _, retained := range []string{"openclaw", "hermes", "picoclaw", "nanobot"} {
				if strings.Contains(msg, retained) {
					return
				}
			}
			t.Errorf("error should name at least one supported runner to migrate to, got %q", msg)
		})
	}
}

func TestRetiredRunnersAreNotRegistered(t *testing.T) {
	for _, name := range []string{"nanoclaw", "microclaw", "nullclaw"} {
		if _, ok := Registered()[name]; ok {
			t.Errorf("retired runner %s is still registered", name)
		}
	}
}

// An unrelated unknown type keeps the generic message; only the runners we
// deliberately dropped get migration guidance.
func TestLookupKeepsGenericErrorForUnknownRunner(t *testing.T) {
	_, err := Lookup("not-a-runner")
	if err == nil {
		t.Fatal("expected unknown runner to be rejected")
	}
	if strings.Contains(err.Error(), "retired") {
		t.Errorf("unknown runner should not be reported as retired, got %q", err)
	}
}
