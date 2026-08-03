package main

import (
	"testing"
	"time"
)

func TestWindowIndexBucketsByAge(t *testing.T) {
	now := time.Date(2026, time.August, 3, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		created time.Time
		want    int
		ok      bool
	}{
		{"just now lands in the newest window", now, 0, true},
		{"one day old is still the newest window", now.AddDate(0, 0, -1), 0, true},
		{"boundary belongs to the older window", now.AddDate(0, 0, -30), 1, true},
		{"one day inside the second window", now.AddDate(0, 0, -31), 1, true},
		{"last day of the oldest window counts", now.AddDate(0, 0, -119), 3, true},
		{"past the oldest window is dropped", now.AddDate(0, 0, -120), 0, false},
		{"future timestamps are dropped", now.AddDate(0, 0, 1), 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := windowIndex(now, tc.created, 30, 4)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Fatalf("index = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestWindowIndexRejectsNonPositiveConfiguration(t *testing.T) {
	now := time.Date(2026, time.August, 3, 0, 0, 0, 0, time.UTC)
	if _, ok := windowIndex(now, now, 0, 4); ok {
		t.Fatal("expected zero window width to be rejected")
	}
	if _, ok := windowIndex(now, now, 30, 0); ok {
		t.Fatal("expected zero window count to be rejected")
	}
}
