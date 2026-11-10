package main

import (
	"bytes"
	"context"
	"errors"
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
		"provider not supported":     {modelfamily.Family{Name: "terra", Provider: "vercel", Pattern: "openai/gpt-*-terra", MaxInput: 4, MaxOutput: 18}, key, "vercel/@terra", &full, false, "not supported for provider", false},
		"plan route":                 {modelfamily.Family{Name: "terra", Provider: "chatgpt", Pattern: "gpt-*-terra", MaxInput: 4, MaxOutput: 18}, key, "chatgpt/@terra", &full, false, "not supported for provider", false},
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
		"no attestation at all":         {func(p *pod.DataPolicyConfig) { p.Attestations = nil }, "no no-training attestation"},
		"attested for another provider": {func(p *pod.DataPolicyConfig) { p.Attestations[0].Provider = "xai" }, "no no-training attestation"},
		"attested for another endpoint": {func(p *pod.DataPolicyConfig) { p.Attestations[0].Endpoint = "https://eu.api.openai.com/v1" }, "no no-training attestation"},
		"no retention guarantee":        {func(p *pod.DataPolicyConfig) { p.Attestations[0].Guarantees = []string{"no-training", "processing:us"} }, "no zero-data-retention attestation"},
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
	for _, endpoint := range []string{"https://api.openai.com/v1", "https://API.openai.com:443/v1/"} {
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
