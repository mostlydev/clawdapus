package main

import (
	"strings"
	"testing"
	"time"

	schedulepkg "github.com/mostlydev/clawdapus/internal/schedule"
)

func TestNextSlotDisplayNotesCoalescedSlots(t *testing.T) {
	now := time.Date(2026, time.July, 2, 15, 0, 0, 0, time.UTC)
	nextFire := now.Add(time.Minute)
	suppressed := now.Add(-time.Minute)
	inv := scheduleInvocationView{
		ManifestInvocation: schedulepkg.ManifestInvocation{
			ID:       "opening-bell",
			Schedule: "* * * * *",
			Timezone: "UTC",
		},
		State: schedulepkg.InvocationState{
			NextFireAt:       &nextFire,
			SuppressedSlots:  3,
			LastSuppressedAt: &suppressed,
			LastStatus:       "fired",
		},
	}

	display := buildNextSlotDisplay(inv, now)
	joined := strings.Join(display.Notes, " | ")
	if !strings.Contains(joined, "3") {
		t.Fatalf("expected coalesced-slot count in notes, got %q", joined)
	}
	if !strings.Contains(strings.ToLower(joined), "wake") {
		t.Fatalf("expected note to explain the overlapping wake, got %q", joined)
	}
}

func TestNextSlotDisplayOmitsCoalescedNoteWhenNoneSuppressed(t *testing.T) {
	now := time.Date(2026, time.July, 2, 15, 0, 0, 0, time.UTC)
	nextFire := now.Add(time.Minute)
	inv := scheduleInvocationView{
		ManifestInvocation: schedulepkg.ManifestInvocation{
			ID:       "opening-bell",
			Schedule: "* * * * *",
			Timezone: "UTC",
		},
		State: schedulepkg.InvocationState{
			NextFireAt: &nextFire,
			LastStatus: "fired",
		},
	}

	display := buildNextSlotDisplay(inv, now)
	for _, note := range display.Notes {
		if strings.Contains(strings.ToLower(note), "coalesc") {
			t.Fatalf("unexpected coalesced note for a healthy schedule: %q", note)
		}
	}
}
