package driver

import "testing"

func TestLookupUnknownTypeReturnsError(t *testing.T) {
	_, err := Lookup("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown driver type")
	}
}

func TestRegisterAndLookup(t *testing.T) {
	Register("test-driver", &stubDriver{})
	d, err := Lookup("test-driver")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d == nil {
		t.Fatal("expected non-nil driver")
	}
}

type stubDriver struct{}

func (s *stubDriver) Validate(rc *ResolvedClaw) error                                                { return nil }
func (s *stubDriver) Materialize(rc *ResolvedClaw, opts MaterializeOpts) (*MaterializeResult, error)  { return &MaterializeResult{}, nil }
func (s *stubDriver) PostApply(rc *ResolvedClaw, opts PostApplyOpts) error                            { return nil }
func (s *stubDriver) HealthProbe(ref ContainerRef) (*Health, error)                                   { return &Health{OK: true}, nil }
