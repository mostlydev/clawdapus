//go:build spike

package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/mostlydev/clawdapus/internal/cllama"
	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/modelfamily"
	"github.com/mostlydev/clawdapus/internal/runs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSpikeFamilyPriceExport reads the price table from a real published
// cllama image through the same one-off Compose project claw up uses, and
// checks nothing is left behind.
func TestSpikeFamilyPriceExport(t *testing.T) {
	const tag = "v0.16.0"
	if err := exec.Command("docker", "image", "inspect", "ghcr.io/mostlydev/cllama:"+tag).Run(); err != nil {
		if out, err := exec.Command("docker", "pull", "ghcr.io/mostlydev/cllama:"+tag).CombinedOutput(); err != nil {
			t.Skipf("cllama %s not available: %v\n%s", tag, err, out)
		}
	}
	prev := infraCllamaTag
	infraCllamaTag = tag
	defer func() { infraCllamaTag = prev }()

	prices, source, err := loadPinnedCllamaPrices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if source.Tag != tag || !strings.HasPrefix(source.ImageID, "sha256:") || prices.Version != tag {
		t.Fatalf("source = %+v, export version %q", source, prices.Version)
	}
	price, ok := prices.Exact("openai", "gpt-6.1-terra")
	if !ok || price.Input != 1 || price.Output != 5 || price.MaxInput != 2 || price.MaxOutput != 7.5 || price.ContextTiers != "all" {
		t.Fatalf("gpt-6.1-terra = %+v (%v)", price, ok)
	}
	if _, ok := prices.Exact("openai", "gpt-6.1-luna"); ok {
		t.Fatal("an unlisted model has an exact price")
	}
	out, _ := exec.Command("docker", "ps", "-a", "--filter", "name=claw-prices-", "--format", "{{.Names}}").Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("the price export left containers behind: %s", out)
	}
	nets, _ := exec.Command("docker", "network", "ls", "--filter", "name=claw-prices-", "--format", "{{.Name}}").Output()
	if strings.TrimSpace(string(nets)) != "" {
		t.Fatalf("the price export left networks behind: %s", nets)
	}

	// An image without the command is reported as such.
	infraCllamaTag = "v0.15.1"
	if exec.Command("docker", "image", "inspect", "ghcr.io/mostlydev/cllama:v0.15.1").Run() == nil {
		if _, _, err := loadPinnedCllamaPrices(context.Background()); err == nil || !strings.Contains(err.Error(), "v0.16.0 or later") {
			t.Fatalf("older image: %v", err)
		}
	}
}

// This uses the published price export and real Hermes template compiler.
// The only fake is the catalog; admission is exercised without starting a
// worker or granting any provider access.
func TestSpikeFamilyCompiledQueueIdentity(t *testing.T) {
	prev := infraCllamaTag
	infraCllamaTag = "v0.16.0"
	defer func() { infraCllamaTag = prev }()
	prices, source, err := loadPinnedCllamaPrices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rig := newFamilyRig(t, terraFamily, map[string]string{"OPENAI_API_KEY": "synthetic-catalog-only"})
	rig.r.loadPrices = func(context.Context) (*modelfamily.Prices, priceSource, error) { return prices, source, nil }
	contract := filepath.Join(rig.dir, "AGENTS.md")
	if err := os.WriteFile(contract, []byte("Complete the task."), 0600); err != nil {
		t.Fatal(err)
	}
	d, err := driver.Lookup("hermes")
	if err != nil {
		t.Fatal(err)
	}
	compiler := d.(driver.RunWorkerCompiler)
	compile := func(ids []string) *runs.Template {
		t.Helper()
		rig.catalog = modelfamily.Catalog{Complete: true, IDs: ids}
		models, err := rig.up(map[string]string{"primary": "openai/@terra"})
		if err != nil {
			t.Fatal(err)
		}
		rc := &driver.ResolvedClaw{ServiceName: "worker", ClawType: "hermes", AgentHostPath: contract, Cllama: []string{"passthrough"}, Models: models}
		worker, err := compiler.CompileRunWorker(rc, driver.RunWorkerOpts{PodName: "family-test", MaxResultBytes: 4096, Home: &driver.RunHomeSpec{Mount: "/task", MaxBytes: 1 << 20}})
		if err != nil {
			t.Fatal(err)
		}
		tpl := &runs.Template{Name: "worker", Pod: "family-test", ClawType: "hermes", ImageID: "sha256:" + strings.Repeat("a", 64), Worker: *worker, Limits: runs.Limits{MaxTaskBytes: 4096, MaxResultBytes: 4096, MaxRuntimeSeconds: 60, MaxAuthoritySeconds: 3600}}
		ctx := &runs.ContextTemplate{Input: cllama.AgentContextInput{Metadata: cllama.InjectCompiledModelPolicy(map[string]any{}, models)}}
		if err := runs.WriteTemplates(rig.dir, []*runs.Template{tpl}, map[string]*runs.ContextTemplate{"worker": ctx}); err != nil {
			t.Fatal(err)
		}
		got, _, err := runs.LoadTemplate(rig.dir, "worker")
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	first := compile([]string{"gpt-5.6-terra"})
	queued, _ := json.Marshal(map[string]any{"template": "worker", "template_id": first.ID, "task": "queued before catalog change", "home": "task-a", "tools": []string{}})
	_, _, digest, err := runs.CanonicalSpec(queued)
	if err != nil {
		t.Fatal(err)
	}
	rig.again(terraFamily)
	second := compile([]string{"gpt-5.6-terra", "gpt-6.1-terra"})
	if first.ID == second.ID {
		t.Fatal("successor did not change template identity")
	}
	infos, invalid, err := runs.DescribeTemplates(rig.dir)
	if err != nil || len(invalid) != 0 || len(infos) != 1 || infos[0].Profile != "task_home" || infos[0].Models[0].Ref != "openai/gpt-6.1-terra" {
		t.Fatalf("listing: %+v %v %v", infos, invalid, err)
	}
	// Nil embedded methods deliberately panic if admission starts any external
	// lifecycle action: this is an admission test, not a worker execution test.
	external := &familyAdmissionOnly{}
	sup, err := runs.NewSupervisor(runs.Config{PodDir: rig.dir, Executor: external, Revoker: external})
	if err != nil {
		t.Fatal(err)
	}
	key := runs.Key{Installation: "test", RunID: "queued"}
	_, _, err = sup.Launch(context.Background(), runs.LaunchRequest{Key: key, Generation: 1, Spec: queued, SpecDigest: digest, AuthorityExpiresAt: time.Now().Add(time.Minute)})
	var refusal *runs.APIError
	if !errors.As(err, &refusal) || refusal.Code != "template_changed" || refusal.CurrentTemplateID != second.ID {
		t.Fatalf("stale spec: %v", err)
	}
	if rec, err := sup.Get(key); err != nil || rec != nil {
		t.Fatalf("stale spec left record: %+v %v", rec, err)
	}
	fresh, _ := json.Marshal(map[string]any{"template": "worker", "template_id": second.ID, "task": "current", "home": "task-a", "tools": []string{}})
	_, _, freshDigest, _ := runs.CanonicalSpec(fresh)
	rec, _, err := sup.Launch(context.Background(), runs.LaunchRequest{Key: key, Generation: 1, Spec: fresh, SpecDigest: freshDigest, AuthorityExpiresAt: time.Now().Add(time.Minute)})
	if err != nil || rec.TemplateID != second.ID || len(rec.Models) != 1 || rec.Models[0].Ref != "openai/gpt-6.1-terra" {
		t.Fatalf("current spec: %+v %v", rec, err)
	}
}

type familyAdmissionOnly struct {
	runs.Executor
	runs.Revoker
}
