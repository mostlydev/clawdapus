package shared

import "testing"

func TestIsFiveFieldCron(t *testing.T) {
	if !IsFiveFieldCron("15 8 * * 1-5") {
		t.Fatal("expected 5-field cron to be valid")
	}
	if IsFiveFieldCron("@hourly") {
		t.Fatal("expected non-cron shortcut to be invalid")
	}
	if IsFiveFieldCron("0 0 1 * * 2026") {
		t.Fatal("expected 6-field cron to be invalid")
	}
}
