package clawapi

import (
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
	store := &Store{
		Principals: []Principal{{
			Name:     "octopus",
			Token:    "capi_deadbeef",
			Services: []string{"[bad"},
		}},
	}
	if err := validateStore(store); err == nil {
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

func TestValidateStoreRejectsUnknownVerb(t *testing.T) {
	store := &Store{
		Principals: []Principal{{
			Name:  "bad",
			Token: "capi_x",
			Verbs: []string{"fleet.explode"},
		}},
	}
	if err := validateStore(store); err == nil {
		t.Fatal("expected unknown verb error")
	}
}

func TestValidateStoreAcceptsAllKnownVerbs(t *testing.T) {
	store := &Store{
		Principals: []Principal{{
			Name:  "full",
			Token: "capi_x",
			Verbs: AllVerbs,
		}},
	}
	if err := validateStore(store); err != nil {
		t.Fatalf("expected all known verbs to be valid: %v", err)
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
