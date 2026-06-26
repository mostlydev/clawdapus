package main

import (
	"testing"

	"github.com/mostlydev/clawdapus/internal/pod"
)

func TestAgentRuntimeRemindersResolvesPodAndServiceConfig(t *testing.T) {
	p := &pod.Pod{
		Context: &pod.ContextConfig{
			RuntimeReminders: []pod.RuntimeReminderConfig{{
				ID:        "pod-focus",
				Text:      "Pod reminder.",
				Enabled:   true,
				Placement: "before_feeds",
				MaxChars:  800,
				Cadence:   "every_turn",
			}},
		},
		Services: map[string]*pod.Service{
			"inherited": {Claw: &pod.ClawBlock{}},
			"override": {Claw: &pod.ClawBlock{Context: &pod.ContextConfig{
				RuntimeReminders: []pod.RuntimeReminderConfig{{
					ID:        "local-focus",
					Text:      "Local reminder.",
					Enabled:   false,
					Placement: "before_feeds",
					MaxChars:  400,
					Cadence:   "every_turn",
				}},
			}}},
			"suppress": {Claw: &pod.ClawBlock{Context: &pod.ContextConfig{
				RuntimeReminders: []pod.RuntimeReminderConfig{},
			}}},
		},
	}

	inherited := agentRuntimeReminders(p, "inherited")
	if len(inherited) != 1 || inherited[0].ID != "pod-focus" || !inherited[0].Enabled {
		t.Fatalf("expected inherited pod reminder, got %+v", inherited)
	}
	override := agentRuntimeReminders(p, "override")
	if len(override) != 1 || override[0].ID != "local-focus" || override[0].Enabled || override[0].MaxChars != 400 {
		t.Fatalf("expected service reminder override, got %+v", override)
	}
	if suppressed := agentRuntimeReminders(p, "suppress"); suppressed != nil {
		t.Fatalf("expected empty service override to suppress pod reminders, got %+v", suppressed)
	}
}
