package runs

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"gopkg.in/yaml.v3"
)

type meterExec struct {
	*fakeExec
	aux []Container
}

func TestTaskHomeFinalizeRequiresAuxiliaryRemoval(t *testing.T) {
	h := taskHomeHarness(t)
	if _, err := launchTaskHome(h, "a", "task-a"); err != nil {
		t.Fatal(err)
	}
	a := h.advance("a")
	if _, err := h.sup.Cancel(context.Background(), Key{"inst", "a"}, 1); err != nil {
		t.Fatal(err)
	}
	h.exec.mu.Lock()
	delete(h.exec.containers, a.RunKey)
	h.exec.mu.Unlock()
	if err := h.sup.FinalizeWithoutProxy(context.Background(), a.Key, func(context.Context) (bool, error) { return true, nil }); err == nil {
		t.Fatal("finalized while auxiliaries remained")
	}
	h.exec.mu.Lock()
	delete(h.exec.aux, a.RunKey)
	h.exec.mu.Unlock()
	if err := h.sup.FinalizeWithoutProxy(context.Background(), a.Key, func(context.Context) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
}

type unavailableMeterExec struct{ *fakeExec }

func (e *unavailableMeterExec) AuxContainers(context.Context, string) ([]Container, error) {
	return nil, errors.New("inspection unavailable")
}

func TestTaskHomeMeterInspectionFailureFencesAuthority(t *testing.T) {
	h := taskHomeHarness(t)
	if _, err := launchTaskHome(h, "a", "task-a"); err != nil {
		t.Fatal(err)
	}
	h.advance("a")
	h.sup.cfg.Executor = &unavailableMeterExec{h.exec}
	a := h.advance("a")
	if a.Failure != ReasonHomeMeterUnavailable || !a.Revoked {
		t.Fatalf("lost monitoring did not fence authority: %+v", a)
	}
	if a.State.Terminal() {
		t.Fatal("terminal claimed without auxiliary-removal proof")
	}
}

func (e *meterExec) AuxContainers(context.Context, string) ([]Container, error) { return e.aux, nil }
func (e *meterExec) Down(ctx context.Context, project, file string) error {
	e.aux = nil
	return e.fakeExec.Down(ctx, project, file)
}

func TestTaskHomeMeterIsConfined(t *testing.T) {
	h := taskHomeHarness(t)
	if _, err := launchTaskHome(h, "a", "task-a"); err != nil {
		t.Fatal(err)
	}
	h.advance("a")
	var doc map[string]any
	if err := yaml.Unmarshal(h.exec.lastFile, &doc); err != nil {
		t.Fatal(err)
	}
	services := doc["services"].(map[string]any)
	meter, ok := services["meter"].(map[string]any)
	if !ok {
		t.Fatal("task-home storage meter is missing")
	}
	if meter["network_mode"] != "none" || meter["read_only"] != true || meter["user"] != workerUser {
		t.Fatalf("unconfined meter: %#v", meter)
	}
	if _, ok := meter["networks"]; ok {
		t.Fatal("meter has network access")
	}
	volumes, _ := meter["volumes"].([]any)
	if len(volumes) != 1 {
		t.Fatalf("meter mounts: %#v", volumes)
	}
	v := volumes[0].(map[string]any)
	if v["read_only"] != true {
		t.Fatal("meter can write home")
	}
	for k := range meter["environment"].(map[string]any) {
		if k == "CLAW_RUN_TOKEN" || k == "CLAW_RUN_TASK" {
			t.Fatal("meter received worker inputs")
		}
	}
}

func TestTaskHomeMeterExcessRevokesAndRemoves(t *testing.T) {
	h := taskHomeHarness(t)
	e := &meterExec{fakeExec: h.exec, aux: []Container{{Running: true, Labels: map[string]string{LabelRole: "run-meter"}}}}
	h.sup.cfg.Executor = e
	if _, err := launchTaskHome(h, "a", "task-a"); err != nil {
		t.Fatal(err)
	}
	a := h.advance("a")
	if a.State != StateRunning {
		t.Fatalf("not running: %+v", a)
	}
	e.aux[0].Running = false
	e.aux[0].Exited = true
	e.aux[0].ExitCode = 75
	a = h.advance("a")
	if a.Failure != "home_full" || !a.Revoked || !a.State.Terminal() || h.exec.count(a.RunKey) != 0 {
		t.Fatalf("meter excess did not fence and remove run: %+v", a)
	}
}

func TestTaskHomeMeterLossFailsClosed(t *testing.T) {
	for _, code := range []int{0, 1, 71} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			h := taskHomeHarness(t)
			e := &meterExec{fakeExec: h.exec, aux: []Container{{Running: true, Labels: map[string]string{LabelRole: "run-meter"}}}}
			h.sup.cfg.Executor = e
			if _, err := launchTaskHome(h, "a", "task-a"); err != nil {
				t.Fatal(err)
			}
			h.advance("a")
			e.aux[0].Running = false
			e.aux[0].Exited = true
			e.aux[0].ExitCode = code
			a := h.advance("a")
			if a.Failure != "home_meter_unavailable" || !a.Revoked || !a.State.Terminal() {
				t.Fatalf("failed meter left run active: %+v", a)
			}
		})
	}
}
