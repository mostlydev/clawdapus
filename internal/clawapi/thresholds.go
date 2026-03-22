package clawapi

import (
	"fmt"
	"os"
	"strconv"

	"github.com/mostlydev/clawdapus/internal/audit"
)

type ThresholdAlert struct {
	Type     string `json:"type"`
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
}

type Thresholds struct {
	ErrorRatePercent     float64 `json:"error_rate_percent"`
	CostPerHourUSD       float64 `json:"cost_per_hour_usd"`
	FeedErrorRatePercent float64 `json:"feed_error_rate_percent"`
	InterventionCount    int     `json:"intervention_count"`
}

func DefaultThresholds() Thresholds {
	return Thresholds{
		ErrorRatePercent:     5.0,
		CostPerHourUSD:       10.0,
		FeedErrorRatePercent: 20.0,
		InterventionCount:    5,
	}
}

func ThresholdsFromEnv() Thresholds {
	th := DefaultThresholds()
	if v := os.Getenv("CLAW_ALERT_ERROR_RATE_PERCENT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			th.ErrorRatePercent = f
		}
	}
	if v := os.Getenv("CLAW_ALERT_COST_PER_HOUR_USD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			th.CostPerHourUSD = f
		}
	}
	if v := os.Getenv("CLAW_ALERT_FEED_ERROR_RATE_PERCENT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			th.FeedErrorRatePercent = f
		}
	}
	if v := os.Getenv("CLAW_ALERT_INTERVENTION_COUNT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			th.InterventionCount = n
		}
	}
	return th
}

func (th Thresholds) Evaluate(agent audit.AgentSummary) []ThresholdAlert {
	var alerts []ThresholdAlert

	if agent.Requests > 0 {
		errorRate := float64(agent.Errors) / float64(agent.Requests) * 100
		if errorRate > th.ErrorRatePercent {
			alerts = append(alerts, ThresholdAlert{
				Type:     "error_rate",
				Severity: "warning",
				Summary:  fmt.Sprintf("%s error rate %.1f%% exceeds %.1f%% threshold (%d errors / %d requests)", agent.ClawID, errorRate, th.ErrorRatePercent, agent.Errors, agent.Requests),
			})
		}
	}

	if th.CostPerHourUSD > 0 && agent.CostUSD > th.CostPerHourUSD {
		alerts = append(alerts, ThresholdAlert{
			Type:     "cost",
			Severity: "warning",
			Summary:  fmt.Sprintf("%s cost $%.2f exceeds $%.2f threshold in window", agent.ClawID, agent.CostUSD, th.CostPerHourUSD),
		})
	}

	if agent.FeedFetches > 0 {
		feedErrorRate := float64(agent.FeedErrors) / float64(agent.FeedFetches) * 100
		if feedErrorRate > th.FeedErrorRatePercent {
			alerts = append(alerts, ThresholdAlert{
				Type:     "feed_error_rate",
				Severity: "warning",
				Summary:  fmt.Sprintf("%s feed error rate %.1f%% exceeds %.1f%% threshold (%d errors / %d fetches)", agent.ClawID, feedErrorRate, th.FeedErrorRatePercent, agent.FeedErrors, agent.FeedFetches),
			})
		}
	}

	if th.InterventionCount > 0 && agent.Interventions >= th.InterventionCount {
		alerts = append(alerts, ThresholdAlert{
			Type:     "interventions",
			Severity: "warning",
			Summary:  fmt.Sprintf("%s recorded %d intervention(s), threshold is %d", agent.ClawID, agent.Interventions, th.InterventionCount),
		})
	}

	return alerts
}
