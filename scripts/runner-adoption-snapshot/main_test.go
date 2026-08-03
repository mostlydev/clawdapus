package main

import (
	"fmt"
	"reflect"
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

func TestForkWindowsBinarySearchesBoundariesAndCachesPages(t *testing.T) {
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	items := make([]map[string]any, 1000)
	for index := range items {
		// One future timestamp and one timestamp exactly at now exercise the
		// upper bound. Six-hour spacing gives exactly 120 forks per 30 days.
		created := now.Add(6*time.Hour - time.Duration(index)*6*time.Hour)
		items[index] = map[string]any{"created_at": created.Format(time.RFC3339)}
	}

	loads := 0
	got, err := forkWindowsWithLoader(len(items), now, 30, 4, func(page int) ([]map[string]any, error) {
		loads++
		start := (page - 1) * 100
		if start >= len(items) {
			return nil, nil
		}
		end := start + 100
		if end > len(items) {
			end = len(items)
		}
		return items[start:end], nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{120, 120, 120, 120}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fork windows = %v, want %v", got, want)
	}
	if loads > 10 {
		t.Fatalf("loaded %d pages; boundary search should cache and stay logarithmic", loads)
	}
}

func TestForkWindowsCountsPartialLastPageWhenAllForksAreRecent(t *testing.T) {
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	items := make([]map[string]any, 205)
	for index := range items {
		items[index] = map[string]any{"created_at": now.Add(-time.Duration(index) * time.Hour).Format(time.RFC3339)}
	}

	got, err := forkWindowsWithLoader(len(items), now, 30, 1, func(page int) ([]map[string]any, error) {
		start := (page - 1) * 100
		if start >= len(items) {
			return nil, nil
		}
		end := start + 100
		if end > len(items) {
			end = len(items)
		}
		return items[start:end], nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{205}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fork windows = %v, want %v", got, want)
	}
}

func TestForkWindowsRejectsMalformedBoundaryTimestamp(t *testing.T) {
	_, err := forkWindowsWithLoader(1, time.Now().UTC(), 30, 1, func(int) ([]map[string]any, error) {
		return []map[string]any{{"created_at": "not-a-time"}}, nil
	})
	if err == nil {
		t.Fatal("expected malformed timestamp to fail the snapshot")
	}
}

func TestLastPageParsesGitHubLinkHeader(t *testing.T) {
	link := fmt.Sprintf(`<https://api.github.com/repositories/1/commits?per_page=1&page=%d>; rel="last", <https://api.github.com/repositories/1/commits?per_page=1&page=2>; rel="next"`, 34897)
	if got := lastPage(link); got != 34897 {
		t.Fatalf("lastPage = %d, want 34897", got)
	}
}
