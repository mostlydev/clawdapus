package main

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/pod"
	"github.com/mostlydev/clawdapus/internal/schedule"
)

func TestBuildScheduleManifestIncludesImageAndPodOpenClawInvocations(t *testing.T) {
	p := &pod.Pod{Name: "trade-desk"}
	resolved := map[string]*driver.ResolvedClaw{
		"tiverton": {
			ServiceName: "tiverton",
			ClawType:    "openclaw",
			Count:       2,
			Handles:     map[string]*driver.HandleInfo{"discord": {ID: "123"}},
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
					To:       "123456789",
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
	if len(manifest.Invocations) != 4 {
		t.Fatalf("expected image and pod invocations expanded across 2 ordinals, got %d", len(manifest.Invocations))
	}

	first := manifest.Invocations[2]
	if first.Service != "tiverton" {
		t.Fatalf("expected service tiverton, got %q", first.Service)
	}
	if first.Timezone != "America/New_York" {
		t.Fatalf("expected us-equities timezone, got %q", first.Timezone)
	}
	if first.Wake.Adapter != "openclaw-exec" {
		t.Fatalf("expected openclaw adapter, got %q", first.Wake.Adapter)
	}
	want := []string{"/usr/local/bin/claw-openclaw-wake", "podjob01", "Pre-market synthesis", "300", "discord", "123456789"}
	if strings.Join(first.Wake.Command, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("expected openclaw cron wake command, got %v", first.Wake.Command)
	}
	if first.AgentID != "tiverton-0" {
		t.Fatalf("expected first ordinal agent id tiverton-0, got %q", first.AgentID)
	}

	second := manifest.Invocations[3]
	if second.AgentID != "tiverton-1" {
		t.Fatalf("expected second ordinal agent id tiverton-1, got %q", second.AgentID)
	}
	if second.ID == first.ID {
		t.Fatalf("expected per-ordinal manifest ids to differ, got %q", first.ID)
	}
}

func TestBuildScheduleManifestOpenClawWakeIsDirectAndBounded(t *testing.T) {
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
		"/usr/local/bin/claw-openclaw-wake", "podjob01", "Heartbeat", "300", "", "",
	}
	if strings.Join(manifest.Invocations[0].Wake.Command, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("expected direct isolated OpenClaw wake, got %v", manifest.Invocations[0].Wake.Command)
	}
}

func TestBuildScheduleManifestUsesServiceTimezoneWithoutCalendar(t *testing.T) {
	manifest, err := buildScheduleManifest(&pod.Pod{Name: "ops"}, map[string]*driver.ResolvedClaw{
		"bot": {
			ServiceName: "bot",
			ClawType:    "picoclaw",
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
			ClawType:    "picoclaw",
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

func TestDirectTurnDeliveryIsExplicitAndUnambiguous(t *testing.T) {
	rc := &driver.ResolvedClaw{Handles: map[string]*driver.HandleInfo{"discord": {ID: "123"}}}
	task := `do not execute $(touch /tmp/not-evaluated); "quoted"`
	cmd, err := directTurnWakeCommand("/helper", rc, driver.Invocation{ID: "tick", Message: task, To: "123456"}, 300)
	if err != nil || len(cmd) != 6 || cmd[2] != task || cmd[4] != "discord" || cmd[5] != "123456" {
		t.Fatalf("positional task/ID delivery lost: %v %v", cmd, err)
	}
	if _, err := directTurnWakeCommand("/helper", rc, driver.Invocation{To: "unknown-name"}, 300); err == nil {
		t.Fatal("unknown Discord name guessed")
	}
	if _, err := directTurnWakeCommand("/helper", rc, driver.Invocation{To: "unknown-name", Channel: "discord"}, 300); err == nil {
		t.Fatal("explicit platform admitted unknown Discord name")
	}
	rc.Handles["slack"] = &driver.HandleInfo{ID: "456"}
	if _, err := directTurnWakeCommand("/helper", rc, driver.Invocation{To: "123456"}, 300); err == nil {
		t.Fatal("ambiguous platform guessed")
	}
	cmd, err = directTurnWakeCommand("/helper", rc, driver.Invocation{}, 300)
	if err != nil || cmd[4] != "" || cmd[5] != "" {
		t.Fatalf("empty delivery guessed a channel: %v %v", cmd, err)
	}
}
