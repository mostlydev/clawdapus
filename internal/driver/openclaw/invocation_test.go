package openclaw

import (
	"github.com/mostlydev/clawdapus/internal/driver"
	"testing"
)

func TestInvocationTimeoutPreservesNativeDefaultAndOverride(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    int
	}{{"", 300}, {"openclaw config set agents.defaults.timeoutSeconds 900", 900}} {
		rc := &driver.ResolvedClaw{}
		if tc.command != "" {
			rc.Configures = []string{tc.command}
		}
		got, err := InvocationTimeoutSeconds(rc)
		if err != nil || got != tc.want {
			t.Fatalf("timeout %q: %d %v", tc.command, got, err)
		}
	}
}
func TestInvocationTimeoutRejectsInvalidOrUnboundedValues(t *testing.T) {
	for _, v := range []string{"0", "-1", "1.5", "slow", "901"} {
		if _, err := InvocationTimeoutSeconds(&driver.ResolvedClaw{Configures: []string{"openclaw config set agents.defaults.timeoutSeconds " + v}}); err == nil {
			t.Fatalf("invalid timeout accepted: %s", v)
		}
	}
}
