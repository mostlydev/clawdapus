package runs

import (
	"strings"
	"testing"
)

func TestPrivateSubnetAllocation(t *testing.T) {
	subnets, err := parsePrivateSubnetBase("")
	if err != nil || subnets.count != 8192 {
		t.Fatalf("default range: %+v %v", subnets, err)
	}
	if got := subnets.at(0); got != "10.213.0.0/29" {
		t.Fatalf("first block = %s", got)
	}
	if got := subnets.at(1); got != "10.213.0.8/29" {
		t.Fatalf("second block = %s", got)
	}
	if got := subnets.at(8191); got != "10.213.255.248/29" {
		t.Fatalf("last block = %s", got)
	}
	// Blocks never repeat while held, and a pod keeps its own starting point.
	used := map[string]bool{}
	first, _ := subnets.allocate("pod-a", used)
	for i := 0; i < 500; i++ {
		subnet, err := subnets.allocate("pod-a", used)
		if err != nil || used[subnet] || !strings.HasPrefix(subnet, "10.213.") {
			t.Fatalf("allocation %d: %s %v", i, subnet, err)
		}
		used[subnet] = true
	}
	if other, _ := subnets.allocate("pod-b", map[string]bool{}); other == first {
		t.Fatal("two pods start at the same block")
	}
	small, _ := parsePrivateSubnetBase("192.168.200.0/24")
	if small.count != 32 {
		t.Fatalf("a /24 holds %d blocks", small.count)
	}
	full := map[string]bool{}
	for i := uint32(0); i < small.count; i++ {
		full[small.at(i)] = true
	}
	if _, err := small.allocate("pod", full); err == nil {
		t.Fatal("allocated from a full range")
	}
	for _, bad := range []string{"8.8.0.0/16", "10.0.0.0/30", "fd00::/64", "nonsense", "10.0.0.0/4"} {
		if _, err := parsePrivateSubnetBase(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

// Concurrent home runs each get their own small block, a finished run's
// block is free again, and the rendered project names it.
func TestTaskHomeRunsGetDistinctSmallSubnets(t *testing.T) {
	h := taskHomeHarness(t)
	seen := map[string]bool{}
	var first *Record
	for i, id := range []string{"a", "b", "c"} {
		rec, err := launchTaskHome(h, id, "home-"+id)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(rec.PrivateSubnet, "/29") || seen[rec.PrivateSubnet] {
			t.Fatalf("run %s subnet %q (seen %v)", id, rec.PrivateSubnet, seen)
		}
		seen[rec.PrivateSubnet] = true
		if i == 0 {
			first = rec
		}
	}
	tpl, _, err := LoadTemplate(h.podDir, "worker")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := renderProject(tpl, first, "/tmp/home")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "subnet: "+first.PrivateSubnet) || !strings.Contains(string(doc), "internal: true") {
		t.Fatalf("rendered project does not name the block:\n%s", doc)
	}
	// A record from before this field renders as it always did.
	old := *first
	old.PrivateSubnet = ""
	doc, _ = renderProject(tpl, &old, "/tmp/home")
	if strings.Contains(string(doc), "ipam") {
		t.Fatalf("a record without a block rendered one:\n%s", doc)
	}
}
