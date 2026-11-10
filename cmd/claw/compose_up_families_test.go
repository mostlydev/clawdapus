package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/modelfamily"
	"github.com/mostlydev/clawdapus/internal/pod"
)

const familyPriceExport = `{"schema":1,"cllama":{"version":"v0.16.0","revision":"abc"},"currency":"USD","unit":"per_million_tokens",
"scope":{"service_tier":"standard","tokens":"uncached_text"},"entries":[
{"provider":"openai","model":"gpt-5.6-terra","input":2,"output":12,"max_input":4,"max_output":18,"context_tiers":"all"},
{"provider":"openai","model":"gpt-6.1-terra","input":1,"output":5,"max_input":2,"max_output":7.5,"context_tiers":"all"}]}`

// attestedPolicy is the default conservative policy with the operator's
// attestations for the openai account.
func attestedPolicy() *pod.DataPolicyConfig {
	policy := pod.DefaultDataPolicy()
	policy.Attestations = []pod.DataPolicyAttestation{{Provider: "openai", Guarantees: []string{"no-training", "zero-data-retention", "processing:us"}}}
	return policy
}

type familyRig struct {
	t         *testing.T
	policy    *pod.DataPolicyConfig
	storeURL  string
	noCllama  bool
	r         *familyResolver
	out       bytes.Buffer
	dir       string
	catalog   modelfamily.Catalog
	catalogOK bool
	fetches   []string // "baseURL key"
	priceRuns int
}

func newFamilyRig(t *testing.T, fam modelfamily.Family, env map[string]string) *familyRig {
	t.Helper()
	rig := &familyRig{t: t, dir: t.TempDir(), catalogOK: true, policy: attestedPolicy()}
	p := &pod.Pod{
		ModelFamilies: []modelfamily.Family{fam},
		Services:      map[string]*pod.Service{"worker": {Claw: &pod.ClawBlock{CllamaEnv: env, DataPolicy: rig.policy}}},
	}
	rig.r = newFamilyResolver(rig.dir, p)
	rig.r.existingBaseURL = func(string) string { return rig.storeURL }
	rig.r.out = &rig.out
	rig.r.now = func() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) }
	rig.r.loadPrices = func(context.Context) (*modelfamily.Prices, priceSource, error) {
		rig.priceRuns++
		prices, err := modelfamily.ParsePrices([]byte(familyPriceExport))
		return prices, priceSource{Tag: "v0.16.0", ImageID: "sha256:img"}, err
	}
	rig.r.fetchCatalog = func(_ context.Context, baseURL, key string) (modelfamily.Catalog, error) {
		rig.fetches = append(rig.fetches, baseURL+" "+key)
		if !rig.catalogOK {
			return modelfamily.Catalog{}, errors.New("model listing could not be read")
		}
		return rig.catalog, nil
	}
	return rig
}

// again builds a second resolver over the same pod directory, as the next
// claw up would, keeping the rig's seams.
func (rig *familyRig) again(fam modelfamily.Family) {
	load, fetch := rig.r.loadPrices, rig.r.fetchCatalog
	p := &pod.Pod{ModelFamilies: []modelfamily.Family{fam},
		Services: map[string]*pod.Service{"worker": {Claw: &pod.ClawBlock{CllamaEnv: rig.r.env, DataPolicy: rig.policy}}}}
	rig.r = newFamilyResolver(rig.dir, p)
	rig.r.existingBaseURL = func(string) string { return rig.storeURL }
	rig.r.out, rig.r.now, rig.r.loadPrices, rig.r.fetchCatalog = &rig.out, func() time.Time { return time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC) }, load, fetch
	rig.out.Reset()
}

func (rig *familyRig) up(models map[string]string) (map[string]string, error) {
	rc := &driver.ResolvedClaw{ServiceName: "worker", Models: models}
	if !rig.noCllama {
		rc.Cllama = []string{"passthrough"}
	}
	if err := rig.r.apply(context.Background(), rc); err != nil {
		return nil, err
	}
	return rc.Models, rig.r.save()
}

func (rig *familyRig) lockEntry(key string) modelfamily.LockEntry {
	lock, err := modelfamily.LoadLock(filepath.Join(rig.dir, modelfamily.LockFileName))
	if err != nil {
		rig.t.Fatal(err)
	}
	return lock.Families[key]
}

var terraFamily = modelfamily.Family{Name: "terra", Provider: "openai", Pattern: "gpt-*-terra", MaxInput: 4, MaxOutput: 18}

func TestExactPinsCauseNoFamilyWork(t *testing.T) {
	rig := newFamilyRig(t, terraFamily, map[string]string{"OPENAI_API_KEY": "sk-key"})
	models, err := rig.up(map[string]string{"primary": "openai/gpt-5.6-terra", "fallback": "chatgpt/gpt-5.6-terra"})
	if err != nil || models["primary"] != "openai/gpt-5.6-terra" || models["fallback"] != "chatgpt/gpt-5.6-terra" {
		t.Fatalf("models = %v, %v", models, err)
	}
	if rig.priceRuns != 0 || len(rig.fetches) != 0 || rig.out.Len() != 0 {
		t.Fatalf("exact pins did work: prices=%d fetches=%v out=%q", rig.priceRuns, rig.fetches, rig.out.String())
	}
	if _, err := os.Stat(filepath.Join(rig.dir, modelfamily.LockFileName)); !os.IsNotExist(err) {
		t.Fatal("a lock was written for a pod of exact pins")
	}
}

func TestFamilyResolvesMovesAndLocks(t *testing.T) {
	rig := newFamilyRig(t, terraFamily, map[string]string{"OPENAI_API_KEY": "sk-key", "OPENAI_API_KEY_1": "sk-backup"})
	rig.catalog = modelfamily.Catalog{Complete: true, IDs: []string{"gpt-5.6-terra", "gpt-5.6-sol"}}

	models, err := rig.up(map[string]string{"primary": "openai/@terra", "fallback": "openai/@terra", "other": "chatgpt/gpt-5.6-terra"})
	if err != nil || models["primary"] != "openai/gpt-5.6-terra" || models["fallback"] != "openai/gpt-5.6-terra" || models["other"] != "chatgpt/gpt-5.6-terra" {
		t.Fatalf("models = %v, %v", models, err)
	}
	// One price read and one listing for two slots, with the primary key at
	// the provider's own endpoint.
	if rig.priceRuns != 1 || len(rig.fetches) != 1 || rig.fetches[0] != "https://api.openai.com/v1 sk-key" {
		t.Fatalf("prices=%d fetches=%v", rig.priceRuns, rig.fetches)
	}
	first := rig.lockEntry("openai/@terra")
	if first.ID != "gpt-5.6-terra" || first.PreviousID != "" || first.Pattern != "gpt-*-terra" || first.MaxInput != 4 || first.MaxOutput != 18 ||
		first.CllamaTag != "v0.16.0" || first.CllamaImageID != "sha256:img" || first.CatalogAt.IsZero() {
		t.Fatalf("lock = %+v", first)
	}
	if !strings.Contains(rig.out.String(), "model family openai/@terra: gpt-5.6-terra") {
		t.Fatalf("output = %q", rig.out.String())
	}
	raw, _ := os.ReadFile(filepath.Join(rig.dir, modelfamily.LockFileName))
	if strings.Contains(string(raw), "sk-key") {
		t.Fatal("the lock holds a key")
	}

	// The vendor ships a successor the pinned cllama prices, and one it does not.
	rig.again(terraFamily)
	rig.catalog = modelfamily.Catalog{Complete: true, IDs: []string{"gpt-5.6-terra", "gpt-6.1-terra", "gpt-7-terra"}}
	models, err = rig.up(map[string]string{"primary": "openai/@terra"})
	if err != nil || models["primary"] != "openai/gpt-6.1-terra" {
		t.Fatalf("models = %v, %v", models, err)
	}
	out := rig.out.String()
	if !strings.Contains(out, "gpt-5.6-terra -> gpt-6.1-terra") || !strings.Contains(out, "not using gpt-7-terra (unpriced)") {
		t.Fatalf("output = %q", out)
	}
	moved := rig.lockEntry("openai/@terra")
	if moved.ID != "gpt-6.1-terra" || moved.PreviousID != "gpt-5.6-terra" || moved.MaxInput != 2 || moved.MaxOutput != 7.5 {
		t.Fatalf("lock = %+v", moved)
	}

	// Unchanged: nothing is announced, and the previous model is kept on record.
	rig.again(terraFamily)
	if _, err = rig.up(map[string]string{"primary": "openai/@terra"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rig.out.String(), "->") || rig.lockEntry("openai/@terra").PreviousID != "gpt-5.6-terra" {
		t.Fatalf("unchanged resolution: out=%q lock=%+v", rig.out.String(), rig.lockEntry("openai/@terra"))
	}
}

func TestFamilyKeepsAValidLockWhenTheListingIsUnavailable(t *testing.T) {
	rig := newFamilyRig(t, terraFamily, map[string]string{"OPENAI_API_KEY": "sk-key"})
	rig.catalog = modelfamily.Catalog{Complete: true, IDs: []string{"gpt-5.6-terra", "gpt-6.1-terra"}}
	if _, err := rig.up(map[string]string{"primary": "openai/@terra"}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(rig.dir, modelfamily.LockFileName))

	// Offline, or an incomplete listing: the locked model stands, the lock
	// is not rewritten.
	for name, setup := range map[string]func(){
		"listing fails": func() { rig.catalogOK = false },
		"listing incomplete": func() {
			rig.catalogOK = true
			rig.catalog = modelfamily.Catalog{Complete: false, IDs: []string{"gpt-9-terra"}}
		},
	} {
		rig.again(terraFamily)
		setup()
		models, err := rig.up(map[string]string{"primary": "openai/@terra"})
		if err != nil || models["primary"] != "openai/gpt-6.1-terra" {
			t.Fatalf("%s: models = %v, %v", name, models, err)
		}
		if !strings.Contains(rig.out.String(), "keeping locked gpt-6.1-terra") {
			t.Fatalf("%s: output = %q", name, rig.out.String())
		}
		after, _ := os.ReadFile(filepath.Join(rig.dir, modelfamily.LockFileName))
		if !bytes.Equal(before, after) {
			t.Fatalf("%s: the lock was rewritten without a complete listing", name)
		}
	}

	// A lock made under looser settings does not survive tightening them.
	rig.catalogOK = false
	for name, fam := range map[string]modelfamily.Family{
		"lower ceiling":   {Name: "terra", Provider: "openai", Pattern: "gpt-*-terra", MaxInput: 1.5, MaxOutput: 18},
		"another pattern": {Name: "terra", Provider: "openai", Pattern: "gpt-*-sol", MaxInput: 4, MaxOutput: 18},
	} {
		rig.again(fam)
		if _, err := rig.up(map[string]string{"primary": "openai/@terra"}); err == nil || !strings.Contains(err.Error(), "no longer qualifies") {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}

func TestFamilyRefusals(t *testing.T) {
	key := map[string]string{"OPENAI_API_KEY": "sk-key"}
	full := modelfamily.Catalog{Complete: true, IDs: []string{"gpt-5.6-terra", "gpt-6.1-terra"}}
	cases := map[string]struct {
		fam     modelfamily.Family
		env     map[string]string
		ref     string
		catalog *modelfamily.Catalog
		offline bool
		want    string
		fetched bool
	}{
		"undeclared family":          {terraFamily, key, "openai/@luna", &full, false, "is not declared", false},
		"family of another provider": {terraFamily, key, "xai/@terra", &full, false, `belongs to provider "openai"`, false},
		"provider not supported":     {modelfamily.Family{Name: "terra", Provider: "openrouter", Pattern: "openai/gpt-*-terra", MaxInput: 4, MaxOutput: 18}, key, "openrouter/@terra", &full, false, "not supported for provider", false},
		"anthropic route":            {modelfamily.Family{Name: "terra", Provider: "anthropic", Pattern: "claude-sonnet-*", MaxInput: 4, MaxOutput: 18}, key, "anthropic/@terra", &full, false, "not supported for provider", false},
		"no key":                     {terraFamily, map[string]string{}, "openai/@terra", &full, false, "no key for provider", false},
		"custom endpoint":            {terraFamily, map[string]string{"OPENAI_API_KEY": "sk-key", "OPENAI_BASE_URL": "https://eu.api.openai.com/v1"}, "openai/@terra", &full, false, "custom endpoint", false},
		"malformed reference":        {terraFamily, key, "openai/@Terra", &full, false, "not a family reference", false},
		"nothing within the ceiling": {modelfamily.Family{Name: "terra", Provider: "openai", Pattern: "gpt-*-terra", MaxInput: 0.5, MaxOutput: 18}, key, "openai/@terra", &full, false, "no model can be chosen", true},
		"first compile offline":      {terraFamily, key, "openai/@terra", nil, true, "there is no models.lock entry", true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rig := newFamilyRig(t, tc.fam, tc.env)
			if tc.catalog != nil {
				rig.catalog = *tc.catalog
			}
			rig.catalogOK = !tc.offline
			_, err := rig.up(map[string]string{"primary": tc.ref})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "sk-key") {
				t.Fatalf("error carries the key: %v", err)
			}
			if (len(rig.fetches) > 0) != tc.fetched {
				t.Fatalf("fetches = %v", rig.fetches)
			}
			if _, statErr := os.Stat(filepath.Join(rig.dir, modelfamily.LockFileName)); !os.IsNotExist(statErr) {
				t.Fatal("a refused resolution wrote a lock")
			}
		})
	}
	// The same official endpoint written with a trailing slash is not custom.
	rig := newFamilyRig(t, terraFamily, map[string]string{"OPENAI_API_KEY": "sk-key", "OPENAI_BASE_URL": "https://api.openai.com/v1/"})
	rig.catalog = full
	if _, err := rig.up(map[string]string{"primary": "openai/@terra"}); err != nil {
		t.Fatal(err)
	}
}

// A service's own data policy is checked every time it uses a family, and a
// family is never chosen for a service cllama does not govern.
func TestFamilyNeedsCllamaAndTheServicePolicy(t *testing.T) {
	full := modelfamily.Catalog{Complete: true, IDs: []string{"gpt-5.6-terra", "gpt-6.1-terra"}}
	for name, tc := range map[string]struct {
		policy func(*pod.DataPolicyConfig)
		want   string
	}{
		"no attestation at all":                 {func(p *pod.DataPolicyConfig) { p.Attestations = nil }, "no no-training attestation"},
		"attested for another provider":         {func(p *pod.DataPolicyConfig) { p.Attestations[0].Provider = "xai" }, "no no-training attestation"},
		"explicit port differs from runtime":    {func(p *pod.DataPolicyConfig) { p.Attestations[0].Endpoint = "https://api.openai.com:443/v1" }, "no no-training attestation"},
		"encoded endpoint differs from runtime": {func(p *pod.DataPolicyConfig) { p.Attestations[0].Endpoint = "https://api.openai.com/%761" }, "no no-training attestation"},
		"attested for another endpoint":         {func(p *pod.DataPolicyConfig) { p.Attestations[0].Endpoint = "https://eu.api.openai.com/v1" }, "no no-training attestation"},
		"no retention guarantee":                {func(p *pod.DataPolicyConfig) { p.Attestations[0].Guarantees = []string{"no-training", "processing:us"} }, "no zero-data-retention attestation"},
		"no region guarantee": {func(p *pod.DataPolicyConfig) {
			p.Attestations[0].Guarantees = []string{"no-training", "zero-data-retention"}
		}, "no processing-region attestation"},
		"region not allowed": {func(p *pod.DataPolicyConfig) {
			p.Attestations[0].Guarantees = []string{"no-training", "zero-data-retention", "processing:jp"}
		}, "no processing-region attestation"},
	} {
		rig := newFamilyRig(t, terraFamily, map[string]string{"OPENAI_API_KEY": "sk-key"})
		rig.catalog = full
		tc.policy(rig.policy)
		if _, err := rig.up(map[string]string{"primary": "openai/@terra"}); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v", name, err)
		}
		if len(rig.fetches) != 0 || rig.priceRuns != 0 {
			t.Errorf("%s: a refused service still read the catalog or prices", name)
		}
	}

	// An attestation that names the official endpoint itself counts.
	for _, endpoint := range []string{"https://api.openai.com/v1", "https://API.openai.com/v1/"} {
		rig := newFamilyRig(t, terraFamily, map[string]string{"OPENAI_API_KEY": "sk-key"})
		rig.catalog = full
		rig.policy.Attestations[0].Endpoint = endpoint
		if models, err := rig.up(map[string]string{"primary": "openai/@terra"}); err != nil || models["primary"] != "openai/gpt-6.1-terra" {
			t.Fatalf("explicit official endpoint %q: %v %v", endpoint, models, err)
		}
	}

	// A permissive policy needs no attestation.
	rig := newFamilyRig(t, terraFamily, map[string]string{"OPENAI_API_KEY": "sk-key"})
	rig.catalog = full
	*rig.policy = pod.DataPolicyConfig{Regions: []string{pod.DataPolicyRegionGlobal}, Training: pod.DataPolicyAllowed, Retention: pod.DataPolicyProviderDefault}
	if models, err := rig.up(map[string]string{"primary": "openai/@terra"}); err != nil || models["primary"] != "openai/gpt-6.1-terra" {
		t.Fatalf("permissive policy: %v %v", models, err)
	}

	// The check is made again for a second service on a cached choice.
	rig.policy.Training, rig.policy.Attestations = pod.DataPolicyNone, nil
	if _, err := rig.up(map[string]string{"primary": "openai/@terra"}); err == nil || !strings.Contains(err.Error(), "no no-training attestation") {
		t.Fatalf("cached choice skipped the policy check: %v", err)
	}

	rig = newFamilyRig(t, terraFamily, map[string]string{"OPENAI_API_KEY": "sk-key"})
	rig.catalog, rig.noCllama = full, true
	if _, err := rig.up(map[string]string{"primary": "openai/@terra"}); err == nil || !strings.Contains(err.Error(), "needs cllama") {
		t.Fatalf("no cllama: %v", err)
	}

	// An endpoint already in the pod's provider store counts as the endpoint.
	rig = newFamilyRig(t, terraFamily, map[string]string{"OPENAI_API_KEY": "sk-key"})
	rig.catalog, rig.storeURL = full, "https://eu.api.openai.com/v1"
	if _, err := rig.up(map[string]string{"primary": "openai/@terra"}); err == nil || !strings.Contains(err.Error(), "custom endpoint") || len(rig.fetches) != 0 {
		t.Fatalf("stored endpoint: %v fetches=%v", err, rig.fetches)
	}
	rig.again(terraFamily)
	rig.storeURL = "https://api.openai.com/v1"
	if _, err := rig.up(map[string]string{"primary": "openai/@terra"}); err != nil {
		t.Fatalf("stored official endpoint: %v", err)
	}
}

func TestFamilyCatalogCannotDowngradeAnEligibleLock(t *testing.T) {
	rig := newFamilyRig(t, terraFamily, map[string]string{"OPENAI_API_KEY": "sk-key"})
	rig.catalog = modelfamily.Catalog{Complete: true, IDs: []string{"gpt-6.1-terra"}}
	if _, err := rig.up(map[string]string{"primary": "openai/@terra"}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(rig.dir, modelfamily.LockFileName))
	for _, ids := range [][]string{{"gpt-5.6-terra"}, {}, {"gpt-7-terra"}} {
		rig.again(terraFamily)
		rig.catalog = modelfamily.Catalog{Complete: true, IDs: ids}
		got, err := rig.up(map[string]string{"primary": "openai/@terra"})
		if err != nil || got["primary"] != "openai/gpt-6.1-terra" {
			t.Fatalf("catalog %v: %v %v", ids, got, err)
		}
		after, _ := os.ReadFile(filepath.Join(rig.dir, modelfamily.LockFileName))
		if !bytes.Equal(before, after) || !strings.Contains(rig.out.String(), "keeping locked gpt-6.1-terra") {
			t.Fatalf("lock changed or no diagnostic: %s", rig.out.String())
		}
	}
}

// The requested names are aligned with the compiled chain, which orders
// slots and drops a repeated model.
func TestRequestedModelChainFollowsThePolicyOrder(t *testing.T) {
	rig := newFamilyRig(t, terraFamily, map[string]string{"OPENAI_API_KEY": "sk-key"})
	rig.catalog = modelfamily.Catalog{Complete: true, IDs: []string{"gpt-5.6-terra", "gpt-6.1-terra"}}
	rc := &driver.ResolvedClaw{ServiceName: "worker", Cllama: []string{"passthrough"}, Models: map[string]string{
		"fallback-2": "openai/@terra", // resolves to the same model as primary and is dropped from the chain
		"primary":    "openai/@terra",
		"fallback":   "chatgpt/gpt-6.1-terra",
		"vision":     "openai/gpt-4o",
	}}
	if err := rig.r.apply(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	if rc.RequestedModels["primary"] != "openai/@terra" || rc.RequestedModels["fallback-2"] != "openai/@terra" || len(rc.RequestedModels) != 2 {
		t.Fatalf("requested = %v", rc.RequestedModels)
	}
	got := requestedModelChain(rc)
	want := []string{"openai/@terra", "", ""} // primary, fallback, vision
	if len(got) != len(want) {
		t.Fatalf("chain = %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chain = %q, want %q", got, want)
		}
	}

	// A claw of exact models has no requested list at all.
	exact := &driver.ResolvedClaw{ServiceName: "worker", Cllama: []string{"passthrough"}, Models: map[string]string{"primary": "openai/gpt-6.1-terra"}}
	if err := rig.r.apply(context.Background(), exact); err != nil {
		t.Fatal(err)
	}
	if exact.RequestedModels != nil || requestedModelChain(exact) != nil {
		t.Fatalf("exact claw: %v %v", exact.RequestedModels, requestedModelChain(exact))
	}
}

// Drift reporting says how each locked model compares with the provider's
// listing now. It reads only.
func TestFamilyDriftReport(t *testing.T) {
	lockPath := func(rig *familyRig) string { return filepath.Join(rig.dir, modelfamily.LockFileName) }
	// seeded returns a rig whose lock holds the given model, resolved from a
	// listing that offered it.
	seeded := func(t *testing.T, locked string) *familyRig {
		rig := newFamilyRig(t, terraFamily, map[string]string{"OPENAI_API_KEY": "sk-key"})
		rig.catalog = modelfamily.Catalog{Complete: true, IDs: []string{locked}}
		if _, err := rig.up(map[string]string{"primary": "openai/@terra"}); err != nil {
			t.Fatal(err)
		}
		return rig
	}
	cases := map[string]struct {
		locked  string
		fam     modelfamily.Family
		catalog modelfamily.Catalog
		offline bool
		want    []string
		not     []string
	}{
		"up to date": {"gpt-6.1-terra", terraFamily, modelfamily.Catalog{Complete: true, IDs: []string{"gpt-5.6-terra", "gpt-6.1-terra"}}, false,
			[]string{"model family openai/@terra: locked gpt-6.1-terra; up to date"}, []string{"would move", "absent"}},
		"newer member available": {"gpt-5.6-terra", terraFamily, modelfamily.Catalog{Complete: true, IDs: []string{"gpt-5.6-terra", "gpt-6.1-terra"}}, false,
			[]string{"locked gpt-5.6-terra; family/pricing candidate gpt-6.1-terra"}, []string{"absent"}},
		"newer member not eligible": {"gpt-6.1-terra", terraFamily, modelfamily.Catalog{Complete: true, IDs: []string{"gpt-6.1-terra", "gpt-7-terra"}}, false,
			[]string{"locked gpt-6.1-terra; newer member gpt-7-terra is not eligible (unpriced)"}, []string{"would move", "absent"}},
		"locked model absent, nothing newer": {"gpt-6.1-terra", terraFamily, modelfamily.Catalog{Complete: true, IDs: []string{"gpt-5.6-terra"}}, false,
			[]string{"locked gpt-6.1-terra; absent from this listing"}, []string{"would move", "retire", "deprecat"}},
		"locked model absent, newer priced": {"gpt-5.6-terra", terraFamily, modelfamily.Catalog{Complete: true, IDs: []string{"gpt-6.1-terra"}}, false,
			[]string{"locked gpt-5.6-terra; absent from this listing; family/pricing candidate gpt-6.1-terra"}, []string{"retire"}},
		"listing unavailable": {"gpt-6.1-terra", terraFamily, modelfamily.Catalog{}, true,
			[]string{"locked gpt-6.1-terra, still meets family/pricing constraints; listing unavailable (model listing could not be read)"}, []string{"would move", "absent"}},
		"listing incomplete": {"gpt-6.1-terra", terraFamily, modelfamily.Catalog{Complete: false, IDs: []string{"gpt-9-terra"}}, false,
			[]string{"locked gpt-6.1-terra, still meets family/pricing constraints; listing unavailable (incomplete)"}, []string{"gpt-9-terra"}},
		"ceiling tightened below the lock": {"gpt-6.1-terra", modelfamily.Family{Name: "terra", Provider: "openai", Pattern: "gpt-*-terra", MaxInput: 1.5, MaxOutput: 18},
			modelfamily.Catalog{Complete: true, IDs: []string{"gpt-5.6-terra", "gpt-6.1-terra"}}, false,
			[]string{"locked gpt-6.1-terra; no longer meets family/pricing constraints (input_price_over_ceiling)"}, []string{"would move"}},
		"version tie in the listing": {"gpt-6-terra-x", terraFamily, modelfamily.Catalog{}, false, nil, nil}, // replaced below
	}
	delete(cases, "version tie in the listing")
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rig := seeded(t, tc.locked)
			before, _ := os.ReadFile(lockPath(rig))
			rig.again(tc.fam)
			rig.catalog, rig.catalogOK = tc.catalog, !tc.offline
			lines := rig.r.familyDrift(context.Background())
			got := strings.Join(lines, "\n")
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("report = %q, want %q", got, want)
				}
			}
			for _, not := range append(tc.not, "sk-key") {
				if strings.Contains(got, not) {
					t.Fatalf("report mentions %q: %q", not, got)
				}
			}
			if len(lines) != 1 {
				t.Fatalf("lines = %q", lines)
			}
			// Nothing was written, and nothing was announced as a move.
			after, _ := os.ReadFile(lockPath(rig))
			if !bytes.Equal(before, after) || rig.r.dirty || rig.out.Len() != 0 {
				t.Fatalf("drift reporting changed state: dirty=%v out=%q", rig.r.dirty, rig.out.String())
			}
		})
	}

	// A pod with no family and no lock reports nothing and reads nothing.
	plain := newFamilyRig(t, terraFamily, map[string]string{"OPENAI_API_KEY": "sk-key"})
	plain.r.families = map[string]modelfamily.Family{}
	if lines := plain.r.familyDrift(context.Background()); len(lines) != 0 || plain.priceRuns != 0 || len(plain.fetches) != 0 {
		t.Fatalf("plain pod: %q", lines)
	}
	if _, err := os.Stat(lockPath(plain)); !os.IsNotExist(err) {
		t.Fatal("drift reporting created a lock file")
	}

	// A declared family that was never resolved, and a lock entry whose
	// family the pod no longer declares.
	fresh := newFamilyRig(t, terraFamily, map[string]string{"OPENAI_API_KEY": "sk-key"})
	if lines := fresh.r.familyDrift(context.Background()); len(lines) != 1 || !strings.Contains(lines[0], "not resolved yet") || len(fresh.fetches) != 0 {
		t.Fatalf("unresolved family: %q", lines)
	}
	orphan := seeded(t, "gpt-6.1-terra")
	orphan.again(modelfamily.Family{Name: "luna", Provider: "openai", Pattern: "gpt-*-luna", MaxInput: 1, MaxOutput: 2})
	lines := orphan.r.familyDrift(context.Background())
	if len(lines) != 2 || !strings.Contains(lines[0], "openai/@luna: declared, not resolved yet") || !strings.Contains(lines[1], "openai/@terra: locked gpt-6.1-terra, but the pod declares no such family") {
		t.Fatalf("removed family: %q", lines)
	}

	// A provider the resolver cannot read, and a custom endpoint, are said so.
	custom := seeded(t, "gpt-6.1-terra")
	custom.again(terraFamily)
	custom.storeURL = "https://eu.api.openai.com/v1"
	if lines := custom.r.familyDrift(context.Background()); len(lines) != 1 || !strings.Contains(lines[0], "listing not read") || len(custom.fetches) != 1 {
		t.Fatalf("custom endpoint: %q fetches=%v", lines, custom.fetches)
	}
}

func TestFamilyDriftDoesNotClaimPolicyAdmission(t *testing.T) {
	for _, tc := range []struct {
		name     string
		declared bool
		policy   *pod.DataPolicyConfig
		want     string
	}{
		{"refusing consumer", true, pod.DefaultDataPolicy(), "service data policy not assessed; claw up rechecks it"},
		{"passing consumer", true, attestedPolicy(), "service data policy not assessed; claw up rechecks it"},
		{"image-only consumer", false, attestedPolicy(), "data policy not assessed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newFamilyRig(t, terraFamily, map[string]string{"OPENAI_API_KEY": "sk-key"})
			rig.catalog = modelfamily.Catalog{Complete: true, IDs: []string{"gpt-5.6-terra"}}
			if _, err := rig.up(map[string]string{"primary": "openai/@terra"}); err != nil {
				t.Fatal(err)
			}
			rig.again(terraFamily)
			rig.r.pod.Services["worker"].Claw.DataPolicy = tc.policy
			if tc.declared {
				rig.r.pod.Services["worker"].Claw.Models = map[string]string{"primary": "openai/@terra"}
			}
			rig.catalog = modelfamily.Catalog{Complete: true, IDs: []string{"gpt-5.6-terra", "gpt-6.1-terra"}}
			before, _ := os.ReadFile(filepath.Join(rig.dir, modelfamily.LockFileName))
			got := strings.Join(rig.r.familyDrift(context.Background()), "\n")
			if !strings.Contains(got, tc.want) || !strings.Contains(got, "family/pricing candidate gpt-6.1-terra") || strings.Contains(got, "claw up would move") {
				t.Fatalf("misleading policy report: %s", got)
			}
			after, _ := os.ReadFile(filepath.Join(rig.dir, modelfamily.LockFileName))
			if !bytes.Equal(before, after) {
				t.Fatal("doctor changed the lock")
			}
		})
	}
}

func vercelPrices(t *testing.T, entries map[string][2]float64) *modelfamily.Prices {
	t.Helper()
	var rows []string
	for id, p := range entries {
		rows = append(rows, fmt.Sprintf(`{"provider":"vercel","model":%q,"input":%v,"output":%v,"max_input":%v,"max_output":%v,"context_tiers":"all"}`, id, p[0], p[1], p[0], p[1]))
	}
	prices, err := modelfamily.ParsePrices([]byte(`{"schema":1,"cllama":{},"currency":"USD","unit":"per_million_tokens","scope":{"service_tier":"standard","tokens":"uncached_text"},"entries":[` + strings.Join(rows, ",") + `]}`))
	if err != nil {
		t.Fatal(err)
	}
	return prices
}

// A gateway family is bounded by the gateway's own prices, including the
// highest price among the providers that serve the model.
func TestVercelFamily(t *testing.T) {
	fam := modelfamily.Family{Name: "terra", Provider: "vercel", Pattern: "openai/gpt-*-terra", MaxInput: 4, MaxOutput: 18}
	policy := func(rig *familyRig) {
		rig.policy.Attestations = []pod.DataPolicyAttestation{{Provider: "vercel", Guarantees: []string{"no-training", "zero-data-retention", "processing:us"}}}
	}
	type endpoint struct {
		in, out float64
		err     error
	}
	setup := func(t *testing.T, endpoints map[string]endpoint) *familyRig {
		rig := newFamilyRig(t, fam, map[string]string{"AI_GATEWAY_API_KEY": "gw-key"})
		policy(rig)
		rig.r.fetchVercel = func(_ context.Context, baseURL, key string) (modelfamily.Catalog, *modelfamily.Prices, error) {
			rig.fetches = append(rig.fetches, baseURL+" "+key)
			if !rig.catalogOK {
				return modelfamily.Catalog{}, nil, errors.New("gateway listing could not be read")
			}
			return rig.catalog, vercelPrices(t, map[string][2]float64{"openai/gpt-5.6-terra": {2, 12}, "openai/gpt-6.1-terra": {2, 7.5}}), nil
		}
		rig.r.fetchVercelEndpointMax = func(_ context.Context, _, _, id string) (float64, float64, error) {
			e := endpoints[id]
			return e.in, e.out, e.err
		}
		rig.catalog = modelfamily.Catalog{Complete: true, IDs: []string{"openai/gpt-5.6-terra", "openai/gpt-6.1-terra", "openai/gpt-7-terra"}}
		return rig
	}

	rig := setup(t, map[string]endpoint{"openai/gpt-6.1-terra": {3, 9, nil}})
	models, err := rig.up(map[string]string{"primary": "vercel/@terra"})
	if err != nil || models["primary"] != "vercel/openai/gpt-6.1-terra" {
		t.Fatalf("models = %v, %v", models, err)
	}
	// The gateway's standard endpoint and key, and no cllama price read.
	if rig.priceRuns != 0 || len(rig.fetches) != 1 || rig.fetches[0] != "https://ai-gateway.vercel.sh/v1 gw-key" {
		t.Fatalf("prices=%d fetches=%v", rig.priceRuns, rig.fetches)
	}
	// The lock records the higher endpoint price and where it came from.
	entry := rig.lockEntry("vercel/@terra")
	if entry.ID != "openai/gpt-6.1-terra" || entry.MaxInput != 3 || entry.MaxOutput != 9 || entry.PriceSource != "catalog" || entry.CllamaTag != "" {
		t.Fatalf("lock = %+v", entry)
	}
	if !strings.Contains(rig.out.String(), "not using openai/gpt-7-terra (unpriced)") {
		t.Fatalf("output = %q", rig.out.String())
	}

	// An endpoint dearer than the ceiling rules the newer model out.
	rig = setup(t, map[string]endpoint{"openai/gpt-6.1-terra": {5, 9, nil}, "openai/gpt-5.6-terra": {2, 12, nil}})
	models, err = rig.up(map[string]string{"primary": "vercel/@terra"})
	if err != nil || models["primary"] != "vercel/openai/gpt-5.6-terra" || !strings.Contains(rig.out.String(), "not using openai/gpt-6.1-terra (input_price_over_ceiling)") {
		t.Fatalf("models = %v, %v, out=%q", models, err, rig.out.String())
	}

	// Endpoint prices that cannot be read whole rule a model out too.
	rig = setup(t, map[string]endpoint{"openai/gpt-6.1-terra": {0, 0, errors.New("gateway endpoint listing has an endpoint with no price")}, "openai/gpt-5.6-terra": {2, 12, nil}})
	models, err = rig.up(map[string]string{"primary": "vercel/@terra"})
	if err != nil || models["primary"] != "vercel/openai/gpt-5.6-terra" || !strings.Contains(rig.out.String(), "not using openai/gpt-6.1-terra (endpoint prices unavailable or over ceiling)") {
		t.Fatalf("models = %v, %v, out=%q", models, err, rig.out.String())
	}
	rig = setup(t, map[string]endpoint{"openai/gpt-6.1-terra": {0, 0, errors.New("x")}, "openai/gpt-5.6-terra": {0, 0, errors.New("x")}})
	if _, err := rig.up(map[string]string{"primary": "vercel/@terra"}); err == nil || !strings.Contains(err.Error(), "no model can be chosen") {
		t.Fatalf("no readable endpoint prices: %v", err)
	}

	// A fresh failed endpoint check cannot be replaced with an old lock price.
	rig = setup(t, map[string]endpoint{"openai/gpt-6.1-terra": {0, 0, errors.New("unavailable")}, "openai/gpt-5.6-terra": {2, 12, nil}})
	rig.r.lock = &modelfamily.Lock{Version: 1, Families: map[string]modelfamily.LockEntry{"vercel/@terra": {Provider: "vercel", Pattern: fam.Pattern, ID: "openai/gpt-6.1-terra", MaxInput: 2, MaxOutput: 7.5, PriceSource: "catalog"}}}
	models, err = rig.up(map[string]string{"primary": "vercel/@terra"})
	if err != nil || models["primary"] != "vercel/openai/gpt-5.6-terra" {
		t.Fatalf("stale endpoint price reused: %v %v", models, err)
	}

	// The bounded lookup loop must not admit the next unchecked candidate.
	rig = setup(t, nil)
	rates := map[string][2]float64{}
	var ids []string
	for i := 1; i <= 17; i++ {
		id := fmt.Sprintf("openai/gpt-%d-terra", i)
		ids = append(ids, id)
		rates[id] = [2]float64{1, 1}
	}
	rig.r.fetchVercel = func(context.Context, string, string) (modelfamily.Catalog, *modelfamily.Prices, error) {
		return modelfamily.Catalog{Complete: true, IDs: ids}, vercelPrices(t, rates), nil
	}
	calls := 0
	rig.r.fetchVercelEndpointMax = func(context.Context, string, string, string) (float64, float64, error) {
		calls++
		return 0, 0, errors.New("unavailable")
	}
	if _, err := rig.up(map[string]string{"primary": "vercel/@terra"}); err == nil || calls != 16 {
		t.Fatalf("unchecked candidate admitted: calls=%d err=%v", calls, err)
	}

	// Listing unavailable: the locked model stands on its recorded price,
	// unless the ceiling has since been lowered below it.
	rig = setup(t, map[string]endpoint{"openai/gpt-6.1-terra": {3, 9, nil}})
	if _, err := rig.up(map[string]string{"primary": "vercel/@terra"}); err != nil {
		t.Fatal(err)
	}
	rig.again(fam)
	policy(rig)
	rig.catalogOK = false
	rig.r.fetchVercel = func(context.Context, string, string) (modelfamily.Catalog, *modelfamily.Prices, error) {
		return modelfamily.Catalog{}, nil, errors.New("gateway listing could not be read")
	}
	models, err = rig.up(map[string]string{"primary": "vercel/@terra"})
	if err != nil || models["primary"] != "vercel/openai/gpt-6.1-terra" || !strings.Contains(rig.out.String(), "keeping locked openai/gpt-6.1-terra") {
		t.Fatalf("offline: %v %v %q", models, err, rig.out.String())
	}
	lower := fam
	lower.MaxInput = 2.5
	rig.again(lower)
	policy(rig)
	rig.r.fetchVercel = func(context.Context, string, string) (modelfamily.Catalog, *modelfamily.Prices, error) {
		return modelfamily.Catalog{}, nil, errors.New("gateway listing could not be read")
	}
	if _, err := rig.up(map[string]string{"primary": "vercel/@terra"}); err == nil || !strings.Contains(err.Error(), "input_price_over_ceiling") {
		t.Fatalf("offline with a lowered ceiling: %v", err)
	}

	// A gateway reached at another address is refused.
	rig = newFamilyRig(t, fam, map[string]string{"AI_GATEWAY_API_KEY": "gw-key", "AI_GATEWAY_BASE_URL": "https://gateway.internal/v1"})
	policy(rig)
	if _, err := rig.up(map[string]string{"primary": "vercel/@terra"}); err == nil || !strings.Contains(err.Error(), "custom endpoint") {
		t.Fatalf("custom gateway: %v", err)
	}
}

// A plan family is resolved from the account's own model list, which only
// cllama can read. It has no price ceiling and never touches an API key.
func TestPlanFamily(t *testing.T) {
	fam := modelfamily.Family{Name: "terra", Provider: "chatgpt", Pattern: "gpt-*-terra"}
	if err := fam.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (modelfamily.Family{Name: "terra", Provider: "chatgpt", Pattern: "gpt-*-terra", MaxInput: 4, MaxOutput: 18}).Validate(); err == nil {
		t.Fatal("a plan family accepted a price ceiling")
	}
	setup := func(t *testing.T) (*familyRig, *[]string, *error) {
		rig := newFamilyRig(t, fam, map[string]string{"OPENAI_API_KEY": "sk-key"})
		rig.policy.Attestations = []pod.DataPolicyAttestation{{Provider: "chatgpt", Guarantees: []string{"no-training", "zero-data-retention", "processing:us"}}}
		ids := []string{"gpt-5.6-terra", "gpt-6.1-terra", "gpt-6.1-sol"}
		var failure error
		rig.r.fetchPlanCatalog = func(_ context.Context, podDir, provider string) (modelfamily.Catalog, priceSource, error) {
			rig.fetches = append(rig.fetches, "plan "+provider)
			if failure != nil {
				return modelfamily.Catalog{}, priceSource{}, failure
			}
			return modelfamily.Catalog{Complete: true, IDs: ids}, priceSource{Tag: "v0.18.0", ImageID: "sha256:img"}, nil
		}
		return rig, &ids, &failure
	}

	rig, ids, failure := setup(t)
	models, err := rig.up(map[string]string{"primary": "chatgpt/@terra", "fallback": "openai/gpt-6.1-terra"})
	if err != nil || models["primary"] != "chatgpt/gpt-6.1-terra" || models["fallback"] != "openai/gpt-6.1-terra" {
		t.Fatalf("models = %v, %v", models, err)
	}
	// Only the plan's list was read: no price table, no API-key listing.
	if rig.priceRuns != 0 || len(rig.fetches) != 1 || rig.fetches[0] != "plan chatgpt" {
		t.Fatalf("prices=%d fetches=%v", rig.priceRuns, rig.fetches)
	}
	entry := rig.lockEntry("chatgpt/@terra")
	if entry.ID != "gpt-6.1-terra" || entry.PriceSource != "plan" || entry.MaxInput != 0 || entry.CllamaTag != "v0.18.0" {
		t.Fatalf("lock = %+v", entry)
	}

	// The plan stops listing the locked model and offers only an older one:
	// the lock stands.
	*ids = []string{"gpt-5.6-terra"}
	rig.again(fam)
	rig.r.fetchPlanCatalog = func(context.Context, string, string) (modelfamily.Catalog, priceSource, error) {
		return modelfamily.Catalog{Complete: true, IDs: *ids}, priceSource{}, nil
	}
	if models, err := rig.up(map[string]string{"primary": "chatgpt/@terra"}); err != nil || models["primary"] != "chatgpt/gpt-6.1-terra" || !strings.Contains(rig.out.String(), "keeping locked gpt-6.1-terra") {
		t.Fatalf("absent locked model: %v %v %q", models, err, rig.out.String())
	}

	// Not signed in, or the list cannot be read: the lock stands; with no
	// lock there is nothing to fall back to, least of all an API key.
	*failure = errors.New("the plan's model list could not be read")
	rig.again(fam)
	rig.r.fetchPlanCatalog = func(context.Context, string, string) (modelfamily.Catalog, priceSource, error) {
		return modelfamily.Catalog{}, priceSource{}, *failure
	}
	if models, err := rig.up(map[string]string{"primary": "chatgpt/@terra"}); err != nil || models["primary"] != "chatgpt/gpt-6.1-terra" {
		t.Fatalf("unavailable list with a lock: %v %v", models, err)
	}
	fresh, _, freshFailure := setup(t)
	*freshFailure = errors.New("the plan's model list could not be read")
	if _, err := fresh.up(map[string]string{"primary": "chatgpt/@terra"}); err == nil || !strings.Contains(err.Error(), "plan's model list is unavailable") || fresh.priceRuns != 0 {
		t.Fatalf("unavailable list without a lock: %v", err)
	}

	// The plan offers nothing of the family.
	none, noneIDs, _ := setup(t)
	*noneIDs = []string{"gpt-6.1-sol"}
	if _, err := none.up(map[string]string{"primary": "chatgpt/@terra"}); err == nil || !strings.Contains(err.Error(), "offers no model of this family") {
		t.Fatalf("no member: %v", err)
	}
}

func TestParsePlanCatalog(t *testing.T) {
	got, err := parsePlanCatalog([]byte(`{"provider":"chatgpt","complete":true,"ids":["gpt-5.6-terra","gpt-6.1-terra"],"read_at":"2026-11-10T21:00:00Z"}`), "chatgpt")
	if err != nil || !got.Complete || len(got.IDs) != 2 {
		t.Fatalf("catalog = %+v %v", got, err)
	}
	for name, raw := range map[string]string{
		"not complete":     `{"provider":"chatgpt","complete":false,"ids":["a"]}`,
		"another provider": `{"provider":"openai","complete":true,"ids":["a"]}`,
		"no ids":           `{"provider":"chatgpt","complete":true}`,
		"id not a string":  `{"provider":"chatgpt","complete":true,"ids":[7]}`,
		"repeated":         `{"provider":"chatgpt","complete":true,"ids":["a","a"]}`,
		"trailing":         `{"provider":"chatgpt","complete":true,"ids":["a"]} {}`,
		"empty":            ``,
	} {
		if got, err := parsePlanCatalog([]byte(raw), "chatgpt"); err == nil {
			t.Errorf("%s: accepted %+v", name, got)
		}
	}
}

func TestParsePlanCatalogRejectsOversize(t *testing.T) {
	raw := []byte(`{"provider":"chatgpt","complete":true,"ids":[]}` + strings.Repeat(" ", 1<<20))
	if _, err := parsePlanCatalog(raw, "chatgpt"); err == nil {
		t.Fatal("accepted oversized catalog")
	}
}
