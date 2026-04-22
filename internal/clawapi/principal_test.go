package clawapi

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPrincipalAllowsPodWildcardScope(t *testing.T) {
	principal := Principal{
		Name:  "octopus",
		Token: "capi_deadbeef",
		Verbs: []string{VerbFleetStatus},
		Pods:  []string{"trading-*"},
	}
	if !principal.AllowsVerb(VerbFleetStatus) {
		t.Fatal("expected fleet.status to be allowed")
	}
	if !principal.AllowsService("trading-desk", "westin") {
		t.Fatal("expected pod scope to allow service access")
	}
	if !principal.AllowsClawID("trading-desk", "westin-0") {
		t.Fatal("expected pod scope to allow claw access")
	}
}

func TestPrincipalServiceAndClawScopesMatchGlobs(t *testing.T) {
	principal := Principal{
		Name:     "analyst",
		Token:    "capi_deadbeef",
		Verbs:    []string{VerbFleetLogs, VerbFleetQueryMetrics},
		Services: []string{"crypto-*"},
		ClawIDs:  []string{"crypto-*"},
	}
	if !principal.AllowsService("trading-desk", "crypto-crusher-2") {
		t.Fatal("expected service glob to match")
	}
	if !principal.AllowsClawID("trading-desk", "crypto-crusher-2") {
		t.Fatal("expected claw glob to match")
	}
	if principal.AllowsService("trading-desk", "trade-executor") {
		t.Fatal("did not expect out-of-scope service access")
	}
}

func TestBuildMasterPrincipalHasAllVerbsAndOpaqueToken(t *testing.T) {
	principal, err := BuildMasterPrincipal("trading-desk", "octopus")
	if err != nil {
		t.Fatalf("BuildMasterPrincipal: %v", err)
	}
	if principal.Name != "octopus" {
		t.Fatalf("unexpected principal: %+v", principal)
	}
	for _, v := range AllVerbs {
		if !principal.AllowsVerb(v) {
			t.Fatalf("master principal missing verb %q", v)
		}
	}
	if principal.Token == "" || principal.Token[:5] != "capi_" {
		t.Fatalf("expected opaque claw-api token, got %q", principal.Token)
	}
}

func TestLoadStoreRejectsInvalidGlobPattern(t *testing.T) {
	path := writeStoreFixture(t, `{"principals":[{"name":"octopus","token":"capi_deadbeef","services":["[bad"]}]}`)
	if _, err := LoadStore(path); err == nil {
		t.Fatal("expected invalid pattern error")
	}
}

func TestPrincipalInvalidGlobDoesNotFallbackToLiteralMatch(t *testing.T) {
	principal := Principal{
		Name:     "analyst",
		Token:    "capi_deadbeef",
		Services: []string{"[bad"},
	}
	if principal.AllowsService("trading-desk", "[bad") {
		t.Fatal("did not expect invalid glob to fallback to literal service match")
	}
}

func TestPrincipalComposeServiceScope(t *testing.T) {
	p := Principal{
		Name:            "ops",
		Token:           "capi_x",
		Verbs:           []string{VerbFleetStatus},
		ComposeServices: []string{"worker-0"},
	}
	if !p.AllowsComposeService("trading-desk", "worker-0") {
		t.Fatal("expected compose service match")
	}
	if p.AllowsComposeService("trading-desk", "worker-1") {
		t.Fatal("did not expect worker-1 to match")
	}
}

func TestLoadStoreFiltersUnknownVerbs(t *testing.T) {
	path := writeStoreFixture(t, `{"principals":[{"name":"future","token":"capi_x","verbs":["fleet.status","schedule.pause","fleet.logs"]}]}`)
	store, err := LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore: %v", err)
	}
	if len(store.Principals) != 1 {
		t.Fatalf("expected one principal, got %d", len(store.Principals))
	}
	wantVerbs := []string{VerbFleetStatus, VerbFleetLogs}
	if !reflect.DeepEqual(store.Principals[0].Verbs, wantVerbs) {
		t.Fatalf("verbs=%v want %v", store.Principals[0].Verbs, wantVerbs)
	}
}

func TestLoadStoreWithWarningsReportsUnknownVerbs(t *testing.T) {
	path := writeStoreFixture(t, `{"principals":[{"name":"future","token":"capi_x","verbs":["fleet.status","schedule.pause","fleet.logs"]}]}`)
	store, warnings, err := LoadStoreWithWarnings(path)
	if err != nil {
		t.Fatalf("LoadStoreWithWarnings: %v", err)
	}
	if len(store.Principals) != 1 {
		t.Fatalf("expected one principal, got %d", len(store.Principals))
	}
	wantVerbs := []string{VerbFleetStatus, VerbFleetLogs}
	if !reflect.DeepEqual(store.Principals[0].Verbs, wantVerbs) {
		t.Fatalf("verbs=%v want %v", store.Principals[0].Verbs, wantVerbs)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `ignoring unknown verb "schedule.pause"`) {
		t.Fatalf("expected unknown-verb warning, got %v", warnings)
	}
}

func TestLoadStoreWithWarningsKeepsPrincipalWhenAllVerbsAreUnknown(t *testing.T) {
	path := writeStoreFixture(t, `{"principals":[{"name":"future","token":"capi_x","verbs":["schedule.pause"]}]}`)
	store, warnings, err := LoadStoreWithWarnings(path)
	if err != nil {
		t.Fatalf("LoadStoreWithWarnings: %v", err)
	}
	if len(store.Principals) != 1 {
		t.Fatalf("expected one principal, got %d", len(store.Principals))
	}
	if len(store.Principals[0].Verbs) != 0 {
		t.Fatalf("expected unknown verbs to be dropped, got %v", store.Principals[0].Verbs)
	}
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings, got %v", warnings)
	}
	if !reflect.DeepEqual(store.NormalizationWarnings, warnings) {
		t.Fatalf("expected warnings to be retained on store, got %v want %v", store.NormalizationWarnings, warnings)
	}
	if !strings.Contains(warnings[0], `ignoring unknown verb "schedule.pause"`) {
		t.Fatalf("expected unknown-verb warning, got %v", warnings)
	}
	if !strings.Contains(warnings[1], `has no recognized verbs`) {
		t.Fatalf("expected inert-principal warning, got %v", warnings)
	}
	principal, err := store.ResolveBearer("Bearer capi_x")
	if err != nil {
		t.Fatalf("ResolveBearer: %v", err)
	}
	if principal.Name != "future" {
		t.Fatalf("unexpected principal: %+v", principal)
	}
	if principal.AllowsVerb(VerbFleetStatus) {
		t.Fatalf("did not expect inert principal to authorize fleet.status")
	}
	if !reflect.DeepEqual(store.InertPrincipalNames(), []string{"future"}) {
		t.Fatalf("expected inert principal summary, got %v", store.InertPrincipalNames())
	}
}

func TestPrincipalVersionSkewWarningsWarnForOlderImage(t *testing.T) {
	store := &Store{Principals: []Principal{{
		Name:  "claw-scheduler",
		Token: "capi_sched",
		Verbs: []string{VerbScheduleRead, VerbScheduleControl},
		Pods:  []string{"ops"},
	}}}

	warnings := PrincipalVersionSkewWarnings(store, "ghcr.io/mostlydev/claw-api:v0.4.2")
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings, got %v", warnings)
	}
	if !strings.Contains(warnings[0], `ghcr.io/mostlydev/claw-api:v0.4.2`) || !strings.Contains(warnings[0], `known minimum v0.6.0`) {
		t.Fatalf("expected version warning, got %v", warnings)
	}
}

func TestPrincipalVersionSkewWarningsSkipSupportedImage(t *testing.T) {
	store := &Store{Principals: []Principal{{
		Name:  "claw-scheduler",
		Token: "capi_sched",
		Verbs: []string{VerbScheduleRead, VerbScheduleControl},
		Pods:  []string{"ops"},
	}}}

	if warnings := PrincipalVersionSkewWarnings(store, "ghcr.io/mostlydev/claw-api:v0.6.0"); len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
}

func TestPrincipalVersionSkewWarningsWarnWhenImageTagUnknown(t *testing.T) {
	store := &Store{Principals: []Principal{{
		Name:  "claw-scheduler",
		Token: "capi_sched",
		Verbs: []string{VerbScheduleRead},
		Pods:  []string{"ops"},
	}}}

	warnings := PrincipalVersionSkewWarnings(store, "ghcr.io/mostlydev/claw-api:latest")
	if len(warnings) != 1 || !strings.Contains(warnings[0], `is not version-pinned`) {
		t.Fatalf("expected uncertainty warning, got %v", warnings)
	}
}

func TestPrincipalPodScopeGrantsComposeServiceAccess(t *testing.T) {
	p := Principal{
		Name:  "master",
		Token: "capi_x",
		Verbs: []string{VerbFleetStatus},
		Pods:  []string{"trading-desk"},
	}
	if !p.AllowsComposeService("trading-desk", "worker-0") {
		t.Fatal("expected pod scope to grant compose service access")
	}
}

func TestBuildSelfPrincipalIsReadOnlyAndServiceScoped(t *testing.T) {
	p, err := BuildSelfPrincipal("trading-desk", "analyst")
	if err != nil {
		t.Fatalf("BuildSelfPrincipal: %v", err)
	}
	if p.Name != "analyst" {
		t.Fatalf("unexpected name: %q", p.Name)
	}
	for _, v := range AllWriteVerbs {
		if p.AllowsVerb(v) {
			t.Fatalf("self principal must not have write verb %q", v)
		}
	}
	for _, v := range AllReadVerbs {
		if !p.AllowsVerb(v) {
			t.Fatalf("self principal missing read verb %q", v)
		}
	}
	if !p.AllowsService("trading-desk", "analyst") {
		t.Fatal("expected service-scope match")
	}
	if p.AllowsService("trading-desk", "other") {
		t.Fatal("did not expect other-service access")
	}
	if p.Token == "" || !strings.HasPrefix(p.Token, "capi_") {
		t.Fatalf("expected capi_ token, got %q", p.Token)
	}
}

func TestBuildMasterPrincipalHasAllVerbs(t *testing.T) {
	p, err := BuildMasterPrincipal("trading-desk", "sentinel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, v := range AllVerbs {
		if !p.AllowsVerb(v) {
			t.Fatalf("master principal missing verb %q", v)
		}
	}
}

func TestBuildSchedulerPrincipalIsScheduleScoped(t *testing.T) {
	p, err := BuildSchedulerPrincipal("trading-desk")
	if err != nil {
		t.Fatalf("BuildSchedulerPrincipal: %v", err)
	}
	if p.Name != "claw-scheduler" {
		t.Fatalf("unexpected name: %q", p.Name)
	}
	if !p.AllowsVerb(VerbScheduleRead) || !p.AllowsVerb(VerbScheduleControl) {
		t.Fatalf("expected schedule verbs, got %v", p.Verbs)
	}
	if p.AllowsVerb(VerbFleetStatus) {
		t.Fatalf("did not expect fleet.status in scheduler principal: %v", p.Verbs)
	}
	if !p.AllowsPod("trading-desk") {
		t.Fatalf("expected pod scope, got %+v", p)
	}
}

func TestBuildDashboardPrincipalIsReadOnlyAndPodScoped(t *testing.T) {
	p, err := BuildDashboardPrincipal("trading-desk")
	if err != nil {
		t.Fatalf("BuildDashboardPrincipal: %v", err)
	}
	if p.Name != "claw-dashboard" {
		t.Fatalf("unexpected name: %q", p.Name)
	}
	for _, v := range AllReadVerbs {
		if !p.AllowsVerb(v) {
			t.Fatalf("dashboard principal missing read verb %q", v)
		}
	}
	for _, v := range AllWriteVerbs {
		if p.AllowsVerb(v) {
			t.Fatalf("dashboard principal must not have write verb %q", v)
		}
	}
	if !p.AllowsPod("trading-desk") {
		t.Fatalf("expected pod scope, got %+v", p)
	}
	if p.Token == "" || !strings.HasPrefix(p.Token, "capi_") {
		t.Fatalf("expected capi_ token, got %q", p.Token)
	}
}

func writeStoreFixture(t *testing.T, raw string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "principals.json")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("write store fixture: %v", err)
	}
	return path
}
