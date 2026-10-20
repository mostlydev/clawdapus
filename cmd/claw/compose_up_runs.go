package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mostlydev/clawdapus/internal/cllama"
	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/pod"
	"github.com/mostlydev/clawdapus/internal/runs"
)

// perRunTemplate is a per-run service resolved during claw up. It is
// validated and compiled like any claw, but never emitted into
// compose.generated.yml; `claw runs serve` instantiates it per run.
type perRunTemplate struct {
	rc       *driver.ResolvedClaw
	compiler driver.RunWorkerCompiler
	context  *cllama.AgentContextInput
	imageRef string
	imageID  string // image validated during this claw up
}

func podHasPerRunServices(p *pod.Pod) bool {
	for _, svc := range p.Services {
		if svc.IsPerRun() {
			return true
		}
	}
	return false
}

// acquirePodRunGuard takes the pod's run lock for the duration of claw up or
// claw down. It fails closed while `claw runs serve` holds the lock, and claw
// up also refuses while any run is unfinished: it would rotate .claw-runtime
// and recreate cllama under live workers.
func acquirePodRunGuard(podDir string, p *pod.Pod, refuseLiveRuns bool) (*runs.PodLock, error) {
	if !podHasPerRunServices(p) {
		if _, err := os.Stat(filepath.Join(podDir, runs.StateDirName)); errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
	}
	lock, err := runs.AcquirePodLock(podDir)
	if errors.Is(err, runs.ErrPodLocked) {
		return nil, fmt.Errorf("`claw runs serve` is running for this pod; stop it first (it holds %s)", filepath.Join(podDir, runs.StateDirName, "lock"))
	}
	if err != nil {
		return nil, err
	}
	if refuseLiveRuns {
		live, err := runs.HasLiveRuns(podDir)
		if err != nil {
			lock.Release()
			return nil, fmt.Errorf("per-run ledger unreadable; refusing to rotate the runtime under it: %w", err)
		}
		if live {
			lock.Release()
			return nil, fmt.Errorf("per-run workers are still unfinished; let them end or cancel them, then run `claw runs serve` until they stop, before claw up")
		}
	}
	return lock, nil
}

// runNetworkName derives the pod's per-run network name from its absolute
// directory, so two pods with the same name never share a worker network.
func runNetworkName(podDir string) string {
	abs, err := filepath.Abs(podDir)
	if err != nil {
		abs = podDir
	}
	sum := sha256.Sum256([]byte(abs))
	return "claw-runs-" + hex.EncodeToString(sum[:6])
}

// validatePerRunResolution applies checks that need the resolved claw.
func validatePerRunResolution(name string, rc *driver.ResolvedClaw, d driver.Driver) (driver.RunWorkerCompiler, error) {
	compiler, ok := d.(driver.RunWorkerCompiler)
	if !ok {
		return nil, fmt.Errorf("service %q: CLAW_TYPE %q does not support x-claw.lifecycle: %s (supported: picoclaw)", name, rc.ClawType, pod.LifecyclePerRun)
	}
	if len(rc.Skills) > 0 {
		return nil, fmt.Errorf("service %q: per-run workers do not support skills yet (image or include declared %d)", name, len(rc.Skills))
	}
	if rc.PersonaHostPath != "" {
		return nil, fmt.Errorf("service %q: per-run workers do not support persona", name)
	}
	if len(rc.Invocations) > 0 {
		return nil, fmt.Errorf("service %q: per-run workers do not support INVOKE schedules", name)
	}
	if err := compiler.ValidateRunWorker(rc); err != nil {
		return nil, fmt.Errorf("service %q: validation failed: %w", name, err)
	}
	return compiler, nil
}

// writePerRunTemplates compiles and installs every per-run template. It runs
// only after the pod applied successfully, under the pod run lock.
func writePerRunTemplates(podDir string, p *pod.Pod, templates map[string]*perRunTemplate, proxyType, dashboardPort string) error {
	names := make([]string, 0, len(templates))
	for name := range templates {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*runs.Template, 0, len(names))
	contexts := make(map[string]*runs.ContextTemplate, len(names))
	for _, name := range names {
		t := templates[name]
		svc := p.Services[name]
		spec, err := t.compiler.CompileRunWorker(t.rc, driver.RunWorkerOpts{PodName: p.Name})
		if err != nil {
			return fmt.Errorf("service %q: compile per-run worker: %w", name, err)
		}
		imageID, _ := discoveryImageIdentity(context.Background(), t.imageRef)
		if imageID == "" || imageID != t.imageID {
			return fmt.Errorf("service %q: image %q changed during claw up (validated %s, now %q); rerun claw up", name, t.imageRef, t.imageID, imageID)
		}
		grant := make([]string, 0, len(t.context.Tools))
		for _, tool := range t.context.Tools {
			grant = append(grant, tool.Name)
		}
		limits := svc.Claw.Run
		out = append(out, &runs.Template{
			Name:         name,
			Pod:          p.Name,
			ClawType:     t.rc.ClawType,
			ImageRef:     t.imageRef,
			ImageID:      imageID,
			RunNetwork:   p.RunNetwork,
			CllamaURL:    cllama.ProxyBaseURL(proxyType),
			CllamaAPIURL: "http://127.0.0.1:" + dashboardPort,
			ToolsGrant:   grant,
			Limits: runs.Limits{
				MaxRuntimeSeconds:   int64(limits.MaxRuntime / time.Second),
				MaxAuthoritySeconds: int64(limits.MaxAuthority / time.Second),
				MaxTaskBytes:        limits.MaxTaskBytes,
				MaxResultBytes:      limits.MaxResultBytes,
				Memory:              limits.Memory,
				Pids:                limits.Pids,
			},
			Worker: *spec,
		})
		contexts[name] = &runs.ContextTemplate{Input: *t.context}
	}
	if err := runs.WriteTemplates(podDir, out, contexts); err != nil {
		return err
	}
	if len(names) > 0 {
		fmt.Printf("[claw] per-run templates compiled: %s (serve with `claw runs serve`)\n", strings.Join(names, ", "))
	}
	return nil
}

func sortedPerRunNames(templates map[string]*perRunTemplate) []string {
	names := make([]string, 0, len(templates))
	for name := range templates {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func mergeResolvedWithPerRun(resolved map[string]*driver.ResolvedClaw, templates map[string]*perRunTemplate) map[string]*driver.ResolvedClaw {
	out := make(map[string]*driver.ResolvedClaw, len(resolved)+len(templates))
	for name, rc := range resolved {
		out[name] = rc
	}
	for name, t := range templates {
		out[name] = t.rc
	}
	return out
}
