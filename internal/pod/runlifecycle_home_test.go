package pod

import (
	"strings"
	"testing"
)

func TestParseRunHome(t *testing.T) {
	_, limits, err := parseRunLifecycle("per-run", &rawRunLimits{Home: &rawRunHome{}, Content: []rawRunContent{{Source: "ref/handbook", Target: "/reference/handbook"}}})
	if err != nil {
		t.Fatal(err)
	}
	if limits.Home == nil || limits.Home.Mount != DefaultRunHomeMount || len(limits.Home.Content) != 1 {
		t.Fatalf("limits: %+v", limits)
	}
	if _, limits, err := parseRunLifecycle("per-run", &rawRunLimits{}); err != nil || limits.Home != nil {
		t.Fatalf("stateless run gained a home: %+v %v", limits, err)
	}
	bad := map[string]*rawRunLimits{
		"max-bytes":        {Home: &rawRunHome{MaxBytes: -1}},
		"content requires": {Content: []rawRunContent{{Source: "a", Target: "/a"}}},
		"home.mount":       {Home: &rawRunHome{Mount: "relative"}},
		"outside /tmp":     {Home: &rawRunHome{Mount: "/tmp/home"}},
		"relative to the":  {Home: &rawRunHome{}, Content: []rawRunContent{{Source: "/etc", Target: "/a"}}},
		"clean absolute":   {Home: &rawRunHome{}, Content: []rawRunContent{{Source: "a", Target: "/a/../b"}}},
		"outside the home": {Home: &rawRunHome{}, Content: []rawRunContent{{Source: "a", Target: "/home/task/ref"}}},
		"must be outside":  {Home: &rawRunHome{Mount: "/work/home"}, Content: []rawRunContent{{Source: "a", Target: "/work"}}},
		"overlaps":         {Home: &rawRunHome{}, Content: []rawRunContent{{Source: "a", Target: "/ref"}, {Source: "b", Target: "/ref/b"}}},
	}
	for want, raw := range bad {
		if _, _, err := parseRunLifecycle("per-run", raw); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
}

func TestParseRunHomeWatchdogThreshold(t *testing.T) {
	for _, n := range []int64{0, 1, 4096, 1 << 40} {
		_, limits, err := parseRunLifecycle("per-run", &rawRunLimits{Home: &rawRunHome{MaxBytes: n}})
		if err != nil {
			t.Fatal(err)
		}
		want := n
		if want == 0 {
			want = DefaultRunHomeMaxBytes
		}
		if limits.Home.MaxBytes != want {
			t.Fatalf("max bytes=%d want%d", limits.Home.MaxBytes, want)
		}
	}
	if _, _, err := parseRunLifecycle("per-run", &rawRunLimits{Home: &rawRunHome{MaxBytes: 1<<40 + 1}}); err == nil {
		t.Fatal("excessive threshold accepted")
	}
}
