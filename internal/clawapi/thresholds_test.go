package clawapi

import (
	"testing"

	"github.com/mostlydev/clawdapus/internal/audit"
)

func TestDefaultThresholdsFlagErrors(t *testing.T) {
	th := DefaultThresholds()
	agent := audit.AgentSummary{
		ClawID:   "weston",
		Requests: 100,
		Errors:   6,
	}
	alerts := th.Evaluate(agent)
	if len(alerts) == 0 {
		t.Fatal("expected error rate alert")
	}
	found := false
	for _, a := range alerts {
		if a.Type == "error_rate" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected error_rate alert, got %v", alerts)
	}
}

func TestDefaultThresholdsNoAlertWhenHealthy(t *testing.T) {
	th := DefaultThresholds()
	agent := audit.AgentSummary{
		ClawID:   "weston",
		Requests: 100,
		Errors:   1,
		CostUSD:  0.50,
	}
	alerts := th.Evaluate(agent)
	if len(alerts) != 0 {
		t.Fatalf("expected no alerts for healthy agent, got %v", alerts)
	}
}

func TestThresholdsFlagFeedErrors(t *testing.T) {
	th := DefaultThresholds()
	agent := audit.AgentSummary{
		ClawID:      "weston",
		Requests:    10,
		FeedFetches: 10,
		FeedErrors:  4,
	}
	alerts := th.Evaluate(agent)
	found := false
	for _, a := range alerts {
		if a.Type == "feed_error_rate" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected feed_error_rate alert, got %v", alerts)
	}
}

func TestThresholdsFromEnvOverrides(t *testing.T) {
	t.Setenv("CLAW_ALERT_ERROR_RATE_PERCENT", "10.0")
	t.Setenv("CLAW_ALERT_MAX_COST_USD", "25.0")
	th := ThresholdsFromEnv()
	if th.ErrorRatePercent != 10.0 {
		t.Fatalf("expected error rate 10.0, got %f", th.ErrorRatePercent)
	}
	if th.MaxCostUSD != 25.0 {
		t.Fatalf("expected cost 25.0, got %f", th.MaxCostUSD)
	}
	// Unset vars should keep defaults
	if th.FeedErrorRatePercent != 20.0 {
		t.Fatalf("expected feed error rate 20.0, got %f", th.FeedErrorRatePercent)
	}
}
