package main

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/pod"
	"github.com/mostlydev/clawdapus/internal/schedule"
)

func TestBuildScheduleManifestIncludesOnlyPodOriginInvocations(t *testing.T) {
	p := &pod.Pod{Name: "trade-desk"}
	resolved := map[string]*driver.ResolvedClaw{
		"tiverton": {
			ServiceName: "tiverton",
			ClawType:    "openclaw",
			Count:       2,
			Invocations: []driver.Invocation{
				{
					ID:       "imagejob01",
					Schedule: "0 * * * *",
					Message:  "Image job",
					Origin:   driver.OriginImage,
				},
				{
					ID:       "podjob01",
					Schedule: "15 8 * * 1-5",
					Message:  "Pre-market synthesis",
					Name:     "Pre-market synthesis",
					To:       "alerts-chan",
					Origin:   driver.OriginPod,
					When:     &schedule.When{Calendar: "us-equities", Session: schedule.SessionRegular},
				},
			},
		},
	}

	manifest, err := buildScheduleManifest(p, resolved)
	if err != nil {
		t.Fatalf("buildScheduleManifest returned error: %v", err)
	}
	if manifest.Pod != "trade-desk" {
		t.Fatalf("expected pod name trade-desk, got %q", manifest.Pod)
	}
	if len(manifest.Invocations) != 2 {
		t.Fatalf("expected one pod-origin invocation expanded across 2 ordinals, got %d", len(manifest.Invocations))
	}

	first := manifest.Invocations[0]
	if first.Service != "tiverton" {
		t.Fatalf("expected service tiverton, got %q", first.Service)
	}
	if first.Timezone != "America/New_York" {
		t.Fatalf("expected us-equities timezone, got %q", first.Timezone)
	}
	if first.Wake.Adapter != "openclaw-exec" {
		t.Fatalf("expected openclaw adapter, got %q", first.Wake.Adapter)
	}
	want := []string{"openclaw", "cron", "run", "podjob01"}
	if strings.Join(first.Wake.Command, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("expected openclaw cron wake command, got %v", first.Wake.Command)
	}
	if first.AgentID != "tiverton-0" {
		t.Fatalf("expected first ordinal agent id tiverton-0, got %q", first.AgentID)
	}

	second := manifest.Invocations[1]
	if second.AgentID != "tiverton-1" {
		t.Fatalf("expected second ordinal agent id tiverton-1, got %q", second.AgentID)
	}
	if second.ID == first.ID {
		t.Fatalf("expected per-ordinal manifest ids to differ, got %q", first.ID)
	}
}

func TestBuildScheduleManifestOpenClawWakeUsesNativeJobID(t *testing.T) {
	manifest, err := buildScheduleManifest(&pod.Pod{Name: "ops"}, map[string]*driver.ResolvedClaw{
		"bot": {
			ServiceName: "bot",
			ClawType:    "openclaw",
			Invocations: []driver.Invocation{
				{
					ID:       "podjob01",
					Schedule: "*/10 * * * *",
					Message:  "Heartbeat",
					Origin:   driver.OriginPod,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("buildScheduleManifest returned error: %v", err)
	}
	if len(manifest.Invocations) != 1 {
		t.Fatalf("expected one invocation, got %d", len(manifest.Invocations))
	}
	want := []string{
		"openclaw", "cron", "run", "podjob01",
	}
	if strings.Join(manifest.Invocations[0].Wake.Command, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("expected openclaw wake command to target native cron id, got %v", manifest.Invocations[0].Wake.Command)
	}
}

func TestBuildScheduleManifestUsesServiceTimezoneWithoutCalendar(t *testing.T) {
	manifest, err := buildScheduleManifest(&pod.Pod{Name: "ops"}, map[string]*driver.ResolvedClaw{
		"bot": {
			ServiceName: "bot",
			ClawType:    "nullclaw",
			Timezone:    "America/New_York",
			Invocations: []driver.Invocation{
				{
					ID:       "podjob01",
					Schedule: "*/10 * * * *",
					Message:  "Heartbeat",
					Origin:   driver.OriginPod,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("buildScheduleManifest returned error: %v", err)
	}
	if len(manifest.Invocations) != 1 {
		t.Fatalf("expected one invocation, got %d", len(manifest.Invocations))
	}
	if manifest.Invocations[0].Timezone != "America/New_York" {
		t.Fatalf("expected service timezone, got %q", manifest.Invocations[0].Timezone)
	}
}

func TestBuildScheduleManifestFallsBackToUTCWithoutServiceTimezone(t *testing.T) {
	manifest, err := buildScheduleManifest(&pod.Pod{Name: "ops"}, map[string]*driver.ResolvedClaw{
		"bot": {
			ServiceName: "bot",
			ClawType:    "nullclaw",
			Invocations: []driver.Invocation{
				{
					ID:       "podjob01",
					Schedule: "*/10 * * * *",
					Message:  "Heartbeat",
					Origin:   driver.OriginPod,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("buildScheduleManifest returned error: %v", err)
	}
	if len(manifest.Invocations) != 1 {
		t.Fatalf("expected one invocation, got %d", len(manifest.Invocations))
	}
	if manifest.Invocations[0].Timezone != "UTC" {
		t.Fatalf("expected UTC fallback timezone, got %q", manifest.Invocations[0].Timezone)
	}
}

func TestBuildScheduleManifestWarnsWhenPatternAWakeDropsToRouting(t *testing.T) {
	out, err := captureStdout(t, func() error {
		_, buildErr := buildScheduleManifest(&pod.Pod{Name: "ops"}, map[string]*driver.ResolvedClaw{
			"bot": {
				ServiceName: "bot",
				ClawType:    "picoclaw",
				Invocations: []driver.Invocation{
					{
						ID:       "podjob01",
						Schedule: "*/10 * * * *",
						Message:  "Heartbeat",
						To:       "alerts",
						Origin:   driver.OriginPod,
					},
				},
			},
		})
		return buildErr
	})
	if err != nil {
		t.Fatalf("buildScheduleManifest returned error: %v", err)
	}
	if !strings.Contains(out, `driver "picoclaw" external scheduler wake does not support invoke.to="alerts"`) {
		t.Fatalf("expected routing warning in output, got %q", out)
	}
}
