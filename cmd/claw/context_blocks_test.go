package main

import (
	"testing"

	"github.com/mostlydev/clawdapus/internal/pod"
)

func TestAgentContextBlocksResolvesPodAndServiceConfig(t *testing.T) {
	p := &pod.Pod{
		Context: &pod.ContextConfig{
			Blocks: []pod.ContextBlockConfig{{
				ID:        "pod-focus",
				Kind:      "runtime_motivation",
				Text:      "Pod block.",
				Enabled:   true,
				Placement: "after_feeds",
				MaxChars:  800,
				Cadence:   "every_turn",
			}},
		},
		Services: map[string]*pod.Service{
			"inherited": {Claw: &pod.ClawBlock{}},
			"override": {Claw: &pod.ClawBlock{Context: &pod.ContextConfig{
				Blocks: []pod.ContextBlockConfig{{
					ID:        "local-focus",
					Kind:      "feed_frame",
					Text:      "Local block.",
					Enabled:   false,
					Placement: "before_feeds",
					MaxChars:  400,
					Cadence:   "every_turn",
				}},
			}}},
			"suppress": {Claw: &pod.ClawBlock{Context: &pod.ContextConfig{
				Blocks: []pod.ContextBlockConfig{},
			}}},
		},
	}

	inherited := agentContextBlocks(p, "inherited")
	if len(inherited) != 1 || inherited[0].ID != "pod-focus" || inherited[0].Kind != "runtime_motivation" || inherited[0].Placement != "after_feeds" || !inherited[0].Enabled {
		t.Fatalf("expected inherited pod context block, got %+v", inherited)
	}
	override := agentContextBlocks(p, "override")
	if len(override) != 1 || override[0].ID != "local-focus" || override[0].Kind != "feed_frame" || override[0].Enabled || override[0].MaxChars != 400 {
		t.Fatalf("expected service context block override, got %+v", override)
	}
	if suppressed := agentContextBlocks(p, "suppress"); suppressed != nil {
		t.Fatalf("expected empty service override to suppress pod context blocks, got %+v", suppressed)
	}
}
