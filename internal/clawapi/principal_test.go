package clawapi

import "testing"

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

func TestBuildMasterPrincipalIsReadOnlyAndOpaque(t *testing.T) {
	principal, err := BuildMasterPrincipal("trading-desk", "octopus")
	if err != nil {
		t.Fatalf("BuildMasterPrincipal: %v", err)
	}
	if principal.Name != "octopus" {
		t.Fatalf("unexpected principal: %+v", principal)
	}
	if len(principal.Verbs) != 4 {
		t.Fatalf("expected read verbs only, got %+v", principal.Verbs)
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
