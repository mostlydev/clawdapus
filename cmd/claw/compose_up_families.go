package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/modelfamily"
	"github.com/mostlydev/clawdapus/internal/pod"
)

// familyRefRe is a model slot that names a family: "<provider>/@<family>".
var familyRefRe = regexp.MustCompile(`^([a-z0-9][a-z0-9-]*)/@([a-z][a-z0-9-]*)$`)

// familyCatalogProviders are the providers a family may resolve against in
// this version: API-key providers whose model listing is the plain
// OpenAI-compatible shape and states no prices. A provider whose listing
// carries its own prices, or needs another listing protocol, is refused
// until that is read and bounded properly.
var familyCatalogProviders = map[string]bool{"openai": true, "xai": true, "google": true, "meta": true}

// priceSource is the cllama image a price table was read from.
type priceSource struct {
	Tag     string
	ImageID string
}

// familyResolver resolves the pod's model families once per claw up.
type familyResolver struct {
	podDir   string
	pod      *pod.Pod
	families map[string]modelfamily.Family
	env      map[string]string // merged cllama-env
	// existingBaseURL is the endpoint the pod's provider store already has
	// for a provider, which the seed merge preserves.
	existingBaseURL func(provider string) string
	out             io.Writer
	now             func() time.Time

	// Seams for tests. The defaults read the pinned cllama image and the
	// provider's own endpoint.
	loadPrices   func(ctx context.Context) (*modelfamily.Prices, priceSource, error)
	fetchCatalog func(ctx context.Context, baseURL, apiKey string) (modelfamily.Catalog, error)

	prices   *modelfamily.Prices
	source   priceSource
	lock     *modelfamily.Lock
	resolved map[string]string // lock key -> concrete id
	dirty    bool
}

func newFamilyResolver(podDir string, p *pod.Pod) *familyResolver {
	r := &familyResolver{podDir: podDir, pod: p, families: map[string]modelfamily.Family{}, env: map[string]string{},
		out: os.Stdout, now: time.Now, resolved: map[string]string{}}
	for _, fam := range p.ModelFamilies {
		r.families[fam.Name] = fam
	}
	names := make([]string, 0, len(p.Services))
	for name := range p.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if svc := p.Services[name]; svc != nil && svc.Claw != nil {
			for k, v := range svc.Claw.CllamaEnv {
				if _, exists := r.env[k]; !exists {
					r.env[k] = v
				}
			}
		}
	}
	r.existingBaseURL = func(provider string) string {
		// Only the endpoint is read. Keys in the store are not touched.
		if state := loadExistingProviders(filepath.Join(podDir, ".claw-auth"))[provider]; state != nil {
			return state.BaseURL
		}
		return ""
	}
	r.loadPrices = loadPinnedCllamaPrices
	r.fetchCatalog = func(ctx context.Context, baseURL, apiKey string) (modelfamily.Catalog, error) {
		return modelfamily.FetchCatalog(ctx, nil, baseURL, apiKey)
	}
	return r
}

// apply replaces every family reference in a claw's model slots with the
// concrete model it resolves to. A claw that names no family is untouched
// and causes no I/O, so a pod of exact pins compiles exactly as before.
func (r *familyResolver) apply(ctx context.Context, rc *driver.ResolvedClaw) error {
	slots := make([]string, 0, len(rc.Models))
	for slot := range rc.Models {
		slots = append(slots, slot)
	}
	sort.Strings(slots)
	for _, slot := range slots {
		ref := strings.TrimSpace(rc.Models[slot])
		m := familyRefRe.FindStringSubmatch(ref)
		if m == nil {
			if strings.Contains(ref, "/@") {
				return fmt.Errorf("service %q: model slot %q: %q is not a family reference of the form <provider>/@<family>", rc.ServiceName, slot, ref)
			}
			continue
		}
		// A family is chosen for cllama to serve. Without cllama nothing
		// enforces the pod's data policy on the chosen model.
		if len(rc.Cllama) == 0 {
			return fmt.Errorf("service %q: model slot %q: a model family needs cllama; name an exact model", rc.ServiceName, slot)
		}
		if _, err := r.lookup(m[1], m[2]); err != nil {
			return fmt.Errorf("service %q: model slot %q: %w", rc.ServiceName, slot, err)
		}
		// The service's own data policy is checked on every use, cached
		// choice or not: one service's attestations do not cover another's.
		if err := familyPolicyError(r.servicePolicy(rc.ServiceName), m[1]); err != nil {
			return fmt.Errorf("service %q: model slot %q: %w", rc.ServiceName, slot, err)
		}
		id, err := r.resolve(ctx, m[1], m[2])
		if err != nil {
			return fmt.Errorf("service %q: model slot %q: %w", rc.ServiceName, slot, err)
		}
		rc.Models[slot] = m[1] + "/" + id
	}
	return nil
}

func (r *familyResolver) providerEndpoint(provider string) (baseURL, key string, err error) {
	for _, def := range seedKeyDefs {
		if def.provider == provider {
			if v := strings.TrimSpace(r.env[def.envVar]); v != "" {
				key = v
				break
			}
		}
	}
	if key == "" {
		return "", "", fmt.Errorf("no key for provider %q in x-claw.cllama-env", provider)
	}
	official := defaultBaseURLs[provider]
	// The endpoint cllama will really use: one already in the pod's provider
	// store is kept by the seed merge, and an x-claw.cllama-env override
	// replaces the default.
	effective := []string{r.existingBaseURL(provider)}
	for envKey, prov := range map[string]string{"OPENAI_BASE_URL": "openai", "XAI_BASE_URL": "xai", "GOOGLE_BASE_URL": "google", "META_BASE_URL": "meta"} {
		if prov == provider {
			effective = append(effective, r.env[envKey])
		}
	}
	for _, candidate := range effective {
		if candidate = strings.TrimSpace(candidate); candidate != "" && strings.TrimRight(candidate, "/") != strings.TrimRight(official, "/") {
			// The price table covers the provider's standard endpoint.
			// Another endpoint may carry an uplift it does not state.
			return "", "", fmt.Errorf("provider %q uses a custom endpoint, whose pricing the price table does not cover; name an exact model", provider)
		}
	}
	return official, key, nil
}

// lookup returns the declared family a reference names, if this version can
// resolve it.
func (r *familyResolver) lookup(provider, name string) (modelfamily.Family, error) {
	fam, ok := r.families[name]
	if !ok {
		return fam, fmt.Errorf("model family %q is not declared in x-claw.model-families", name)
	}
	if fam.Provider != provider {
		return fam, fmt.Errorf("model family %q belongs to provider %q, not %q", name, fam.Provider, provider)
	}
	if !familyCatalogProviders[provider] {
		return fam, fmt.Errorf("model families are not supported for provider %q yet; name an exact model", provider)
	}
	return fam, nil
}

func (r *familyResolver) resolve(ctx context.Context, provider, name string) (string, error) {
	key := modelfamily.LockKey(provider, name)
	if id, ok := r.resolved[key]; ok {
		return id, nil
	}
	fam, err := r.lookup(provider, name)
	if err != nil {
		return "", err
	}
	if r.prices == nil {
		prices, source, err := r.loadPrices(ctx)
		if err != nil {
			return "", fmt.Errorf("price table unavailable: %w", err)
		}
		r.prices, r.source = prices, source
	}
	if r.lock == nil {
		lock, err := modelfamily.LoadLock(filepath.Join(r.podDir, modelfamily.LockFileName))
		if err != nil {
			return "", err
		}
		r.lock = lock
	}
	locked, hadLock := r.lock.Families[key]

	baseURL, apiKey, err := r.providerEndpoint(provider)
	if err != nil {
		return "", err
	}
	catalog, catalogErr := r.fetchCatalog(ctx, baseURL, apiKey)
	if catalogErr != nil || !catalog.Complete {
		// Only the catalog's freshness is waived. The locked model is judged
		// against the current family, price table and ceilings.
		why := "incomplete"
		if catalogErr != nil {
			why = catalogErr.Error()
		}
		if !hadLock {
			return "", fmt.Errorf("model family %s: the provider's model listing is unavailable (%s) and there is no %s entry; name an exact model or retry", key, why, modelfamily.LockFileName)
		}
		if reason := fam.Reuse(locked, r.prices, nil); reason != "" {
			return "", fmt.Errorf("model family %s: the provider's model listing is unavailable (%s) and the locked model %s no longer qualifies (%s)", key, why, locked.ID, reason)
		}
		fmt.Fprintf(r.out, "[claw] model family %s: listing unavailable (%s); keeping locked %s\n", key, why, locked.ID)
		r.resolved[key] = locked.ID
		return locked.ID, nil
	}

	decision := fam.Choose(catalog, r.prices, nil)
	for _, s := range decision.Skipped {
		fmt.Fprintf(r.out, "[claw] model family %s: not using %s (%s)\n", key, s.ID, s.Reason)
	}
	// A listing is evidence of availability, not permission to downgrade.
	// Keep a still-eligible lock when no successor qualifies, even if the
	// listing omitted it. Changed operator constraints still take effect:
	// an ineligible lock is not reused.
	if hadLock && fam.Reuse(locked, r.prices, nil) == "" {
		lockedVersion, _ := fam.Member(locked.ID)
		candidateVersion, _ := fam.Member(decision.ID)
		if decision.ID == "" || candidateVersion.Compare(lockedVersion) < 0 {
			fmt.Fprintf(r.out, "[claw] model family %s: no newer member qualifies; keeping locked %s (catalog absence is not retirement)\n", key, locked.ID)
			r.resolved[key] = locked.ID
			return locked.ID, nil
		}
	}
	if decision.ID == "" {
		return "", fmt.Errorf("model family %s: no model can be chosen (%s); adjust the family or name an exact model", key, decision.Reason)
	}
	switch {
	case !hadLock:
		fmt.Fprintf(r.out, "[claw] model family %s: %s\n", key, decision.ID)
	case locked.ID != decision.ID:
		fmt.Fprintf(r.out, "[claw] model family %s: %s -> %s\n", key, locked.ID, decision.ID)
	}
	entry := modelfamily.LockEntry{
		Provider: fam.Provider, Pattern: fam.Pattern, ID: decision.ID,
		MaxInput: decision.Price.MaxInput, MaxOutput: decision.Price.MaxOutput,
		CatalogAt: r.now().UTC().Truncate(time.Second), CllamaTag: r.source.Tag, CllamaImageID: r.source.ImageID,
	}
	if hadLock {
		entry.PreviousID = locked.PreviousID
		if locked.ID != decision.ID {
			entry.PreviousID = locked.ID
		}
	}
	r.lock.Families[key] = entry
	r.dirty = true
	r.resolved[key] = decision.ID
	return decision.ID, nil
}

// save writes the lock if any family was resolved from a live catalog.
func (r *familyResolver) save() error {
	if !r.dirty {
		return nil
	}
	return r.lock.Save(filepath.Join(r.podDir, modelfamily.LockFileName))
}

// loadPinnedCllamaPrices runs "cllama prices --json" from the pinned cllama
// image and parses its output. The image is inspected once and run by that
// ID, so the table read is the table of the image recorded in the lock.
// The process gets no network, no mounts, no environment and no
// credentials. It runs as a one-off Compose project, which is removed.
func loadPinnedCllamaPrices(ctx context.Context) (*modelfamily.Prices, priceSource, error) {
	ref := taggedInfraRef("ghcr.io/mostlydev/cllama", infraCllamaTag)
	source := priceSource{Tag: infraCllamaTag}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	idOut, err := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", ref).Output()
	imageID := strings.TrimSpace(string(idOut))
	if err != nil || !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(imageID) {
		return nil, source, fmt.Errorf("the pinned cllama image %s is not present; run 'claw pull'", ref)
	}
	source.ImageID = imageID

	dir, err := os.MkdirTemp("", "claw-prices-")
	if err != nil {
		return nil, source, err
	}
	defer os.RemoveAll(dir)
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return nil, source, err
	}
	project := "claw-prices-" + hex.EncodeToString(suffix)
	composeFile := filepath.Join(dir, "compose.yml")
	compose := "services:\n  prices:\n    image: " + imageID + "\n" +
		"    entrypoint: [\"/cllama\"]\n    command: [\"prices\", \"--json\"]\n" +
		"    network_mode: none\n    read_only: true\n    cap_drop: [ALL]\n" +
		"    security_opt: [\"no-new-privileges:true\"]\n    pull_policy: never\n    environment: {}\n"
	if err := os.WriteFile(composeFile, []byte(compose), 0o600); err != nil {
		return nil, source, err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		down := exec.CommandContext(cleanupCtx, "docker", "compose", "-p", project, "-f", composeFile, "down", "--remove-orphans")
		down.Env = scrubbedComposeEnv()
		_ = down.Run()
	}()
	run := exec.CommandContext(ctx, "docker", "compose", "-p", project, "-f", composeFile, "run", "--rm", "--no-deps", "-T", "prices")
	run.Env = scrubbedComposeEnv()
	var stdout bytes.Buffer
	run.Stdout = &limitedWriter{w: &stdout, n: modelfamily.MaxPriceExportBytes + 1}
	if err := run.Run(); err != nil {
		return nil, source, fmt.Errorf("the pinned cllama image %s has no price export; it needs cllama v0.16.0 or later", ref)
	}
	prices, err := modelfamily.ParsePrices(stdout.Bytes())
	if err != nil {
		return nil, source, err
	}
	return prices, source, nil
}

// scrubbedComposeEnv is the environment for the price export's Compose
// commands: what Docker needs to find its daemon, and nothing else.
func scrubbedComposeEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch {
		case name == "PATH", name == "HOME", name == "USER", name == "TMPDIR", name == "XDG_RUNTIME_DIR",
			strings.HasPrefix(name, "DOCKER_"):
			env = append(env, kv)
		}
	}
	return env
}

// limitedWriter stops accepting bytes past its limit without failing the
// writer, so an oversized export is detected by its size, not by a hang.
type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		take := p
		if len(take) > l.n {
			take = take[:l.n]
		}
		_, _ = l.w.Write(take)
		l.n -= len(take)
	}
	return len(p), nil
}

// servicePolicy is the data policy a service's model calls are held to.
func (r *familyResolver) servicePolicy(serviceName string) *pod.DataPolicyConfig {
	if r.pod != nil {
		if svc := r.pod.Services[serviceName]; svc != nil && svc.Claw != nil && svc.Claw.DataPolicy != nil {
			return svc.Claw.DataPolicy
		}
	}
	return pod.DefaultDataPolicy()
}

// familyPolicyError checks, at compile time, the part of cllama's data
// policy that depends only on the provider and its official endpoint: a
// constrained policy needs the operator's attestation for that provider. A
// family resolves only against the official endpoint of a provider in
// familyCatalogProviders, so this is the whole provider-level rule for it.
// cllama still enforces the full policy on every request.
func familyPolicyError(policy *pod.DataPolicyConfig, provider string) error {
	guarantees := map[string]bool{}
	for _, a := range policy.Attestations {
		// An attestation with no endpoint covers the official endpoint, and
		// so does one that names it. One naming another endpoint does not.
		if a.Provider == provider && (strings.TrimSpace(a.Endpoint) == "" || sameEndpoint(a.Endpoint, defaultBaseURLs[provider])) {
			for _, g := range a.Guarantees {
				guarantees[g] = true
			}
		}
	}
	if policy.Training == pod.DataPolicyNone && !guarantees["no-training"] {
		return fmt.Errorf("the data policy forbids training and has no no-training attestation for provider %q", provider)
	}
	if policy.Retention == pod.DataPolicyNone && !guarantees["zero-data-retention"] {
		return fmt.Errorf("the data policy forbids retention and has no zero-data-retention attestation for provider %q", provider)
	}
	global := false
	for _, region := range policy.Regions {
		if region == pod.DataPolicyRegionGlobal {
			global = true
		}
	}
	if !global {
		attested := false
		for _, region := range policy.Regions {
			attested = attested || guarantees["processing:"+region]
		}
		if !attested {
			return fmt.Errorf("the data policy limits regions and has no processing-region attestation for provider %q", provider)
		}
	}
	return nil
}

// sameEndpoint compares two endpoint URLs as cllama does: scheme and host
// without case, explicit ports preserved, and no trailing slash.
func sameEndpoint(a, b string) bool {
	norm := func(raw string) (string, bool) {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || u.Opaque != "" {
			return "", false
		}
		host := strings.ToLower(u.Hostname())
		if port := u.Port(); port != "" {
			host += ":" + port
		}
		return strings.ToLower(u.Scheme) + "://" + host + strings.TrimRight(u.Path, "/"), true
	}
	na, okA := norm(a)
	nb, okB := norm(b)
	return okA && okB && na == nb
}
