package schedule

import (
	"testing"
	"time"
)

func TestLookupCalendarRejectsUnknown(t *testing.T) {
	if _, err := LookupCalendar("mars-exchange"); err == nil {
		t.Fatal("expected unknown calendar error")
	}
}

func TestWhenValidateRejectsUnsupportedSession(t *testing.T) {
	when := &When{Calendar: "crypto-24-7", Session: SessionPreMarket}
	if err := when.Validate(); err == nil {
		t.Fatal("expected unsupported session error")
	}
}

func TestUSEquitiesHolidayClosesMarket(t *testing.T) {
	cal, err := LookupCalendar("us-equities")
	if err != nil {
		t.Fatalf("lookup calendar: %v", err)
	}
	ts := time.Date(2026, time.July, 3, 10, 0, 0, 0, time.UTC)
	state := cal.SessionAt(ts)
	if state.Open {
		t.Fatalf("expected market closed on observed July 3 holiday, got %+v", state)
	}
	if state.Reason != "holiday" {
		t.Fatalf("expected holiday reason, got %+v", state)
	}
}

func TestUSEquitiesEarlyCloseTransitions(t *testing.T) {
	cal, err := LookupCalendar("us-equities")
	if err != nil {
		t.Fatalf("lookup calendar: %v", err)
	}

	beforeClose := time.Date(2026, time.November, 27, 17, 30, 0, 0, time.UTC)
	state := cal.SessionAt(beforeClose)
	if !state.Open || state.Name != SessionRegular {
		t.Fatalf("expected early-close regular session, got %+v", state)
	}

	afterClose := time.Date(2026, time.November, 27, 18, 30, 0, 0, time.UTC)
	state = cal.SessionAt(afterClose)
	if !state.Open || state.Name != SessionAfterHours {
		t.Fatalf("expected after-hours after early close, got %+v", state)
	}
}

func TestUSEquitiesDSTBoundaryUsesLocalTimezone(t *testing.T) {
	cal, err := LookupCalendar("us-equities")
	if err != nil {
		t.Fatalf("lookup calendar: %v", err)
	}

	beforeOpen := time.Date(2026, time.March, 9, 13, 25, 0, 0, time.UTC)
	state := cal.SessionAt(beforeOpen)
	if !state.Open || state.Name != SessionPreMarket {
		t.Fatalf("expected pre-market before 09:30 ET after DST shift, got %+v", state)
	}

	atOpen := time.Date(2026, time.March, 9, 13, 30, 0, 0, time.UTC)
	state = cal.SessionAt(atOpen)
	if !state.Open || state.Name != SessionRegular {
		t.Fatalf("expected regular session at 09:30 ET after DST shift, got %+v", state)
	}
}

func TestCrypto247AlwaysOpen(t *testing.T) {
	cal, err := LookupCalendar("crypto-24-7")
	if err != nil {
		t.Fatalf("lookup calendar: %v", err)
	}
	state := cal.SessionAt(time.Date(2026, time.January, 4, 3, 0, 0, 0, time.UTC))
	if !state.Open || state.Name != SessionRegular {
		t.Fatalf("expected crypto market open, got %+v", state)
	}
}
