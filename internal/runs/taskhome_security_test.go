package runs

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// These are deliberately red acceptance cases for #394. JSON fixtures keep
// them compilable before the opt-in schema exists. Admission failures are
// prerequisites, not evidence that the later isolation assertions ran.
func taskHomeHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, false)
	tpl, ctx, err := LoadTemplate(h.podDir, "worker")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(tpl.Worker)
	var worker map[string]any
	if err := json.Unmarshal(raw, &worker); err != nil {
		t.Fatal(err)
	}
	worker["home"] = map[string]any{"mount": "/home/task"}
	raw, _ = json.Marshal(worker)
	if err := json.Unmarshal(raw, &tpl.Worker); err != nil {
		t.Fatal(err)
	}
	tpl.ClawType = "hermes"
	if err := WriteTemplates(h.podDir, []*Template{tpl}, map[string]*ContextTemplate{"worker": ctx}); err != nil {
		t.Fatal(err)
	}
	return h
}

func launchTaskHome(h *harness, id, home string) (*Record, error) {
	raw, _ := json.Marshal(map[string]any{"template": "worker", "task": "task", "tools": []string{}, "home": home})
	_, _, digest, err := CanonicalSpec(raw)
	if err != nil {
		return nil, err
	}
	rec, _, err := h.sup.Launch(context.Background(), LaunchRequest{
		Key: Key{"inst", id}, Generation: 1, Spec: raw, SpecDigest: digest,
		AuthorityExpiresAt: h.now.Add(30 * time.Minute),
	})
	return rec, err
}

func TestTaskHomeSecurityMountOnlyOwnHome(t *testing.T) {
	h := taskHomeHarness(t)
	if _, err := launchTaskHome(h, "a", "task-a"); err != nil {
		t.Fatalf("task-home admission prerequisite: %v", err)
	}
	h.advance("a")
	h.exec.mu.Lock()
	raw := append([]byte(nil), h.exec.lastFile...)
	h.exec.mu.Unlock()
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	services := doc["services"].(map[string]any)
	worker := services["worker"].(map[string]any)
	mounts, _ := worker["volumes"].([]any)
	if len(mounts) != 1 {
		t.Fatalf("worker must mount exactly its own home; got %#v", mounts)
	}
	mount, _ := mounts[0].(map[string]any)
	if mount["target"] != "/home/task" || mount["read_only"] == true {
		t.Fatalf("wrong home mount: %#v", mount)
	}
	// Live hostile reads, symlink probes and marker checks are specified in the
	// companion acceptance document; rendered topology is not filesystem proof.
}

func TestTaskHomeSecurityWorkerHasPrivateNetwork(t *testing.T) {
	h := taskHomeHarness(t)
	if _, err := launchTaskHome(h, "a", "task-a"); err != nil {
		t.Fatalf("task-home admission prerequisite: %v", err)
	}
	h.advance("a")
	h.exec.mu.Lock()
	raw := append([]byte(nil), h.exec.lastFile...)
	h.exec.mu.Unlock()
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	nets := doc["networks"].(map[string]any)
	private := 0
	for _, v := range nets {
		n, _ := v.(map[string]any)
		if n["internal"] == true && n["external"] != true {
			private++
		}
	}
	if private != 1 {
		t.Fatalf("expected one project-private internal worker network: %#v", nets)
	}
	// A live B listener, denied A connection and successful cllama request
	// remain required. This assertion alone cannot prove packet isolation.
}

func TestTaskHomeSecuritySameHomeRace(t *testing.T) {
	h := taskHomeHarness(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, id := range []string{"a", "b"} {
		wg.Add(1)
		go func(id string) { defer wg.Done(); <-start; _, err := launchTaskHome(h, id, "shared"); results <- err }(id)
	}
	close(start)
	wg.Wait()
	close(results)
	accepted, busy := 0, 0
	for err := range results {
		if err == nil {
			accepted++
		} else if apiCode(err) == "home_busy" {
			busy++
		} else {
			t.Fatalf("task-home admission prerequisite: %v", err)
		}
	}
	if accepted != 1 || busy != 1 {
		t.Fatalf("accepted=%d busy=%d; expected 1 each", accepted, busy)
	}
}

func TestTaskHomeSecurityRevokeOutageKeepsReservation(t *testing.T) {
	h := taskHomeHarness(t)
	if _, err := launchTaskHome(h, "a", "shared"); err != nil {
		t.Fatalf("task-home admission prerequisite: %v", err)
	}
	a := h.advance("a")
	h.rev.err = errors.New("revoke unavailable")
	if _, err := h.sup.Cancel(context.Background(), Key{"inst", "a"}, 1); err != nil {
		t.Fatal(err)
	}
	a = h.advance("a")
	if h.exec.count(a.RunKey) != 0 || a.Revoked || a.State.Terminal() {
		t.Fatalf("outage must remove worker without claiming fence: %+v", a)
	}
	if _, err := launchTaskHome(h, "b", "shared"); apiCode(err) != "home_busy" {
		t.Fatalf("home released before ACK: %v", err)
	}
	if _, err := launchTaskHome(h, "c", "unrelated"); err != nil {
		t.Fatalf("unrelated home blocked: %v", err)
	}
	h.rev.err = nil
	h.advance("a")
	if _, err := launchTaskHome(h, "b", "shared"); err != nil {
		t.Fatalf("home not released after removal+ACK: %v", err)
	}
}
