package main

import (
	"testing"

	"github.com/mostlydev/clawdapus/internal/clawapi"
	"github.com/mostlydev/clawdapus/internal/pod"
)

func TestMergePrincipalsAutoOnlyPassthrough(t *testing.T) {
	auto := []clawapi.Principal{
		{Name: "sentinel", Token: "capi_master", Verbs: clawapi.AllVerbs, Pods: []string{"trading-desk"}},
	}
	result, err := mergePrincipals(auto, nil, "trading-desk")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 principal, got %d", len(result))
	}
	if result[0].Principal.Name != "sentinel" {
		t.Fatalf("unexpected name: %q", result[0].Principal.Name)
	}
}

func TestMergePrincipalsExplicitOverridesByName(t *testing.T) {
	auto := []clawapi.Principal{
		{Name: "sentinel", Token: "capi_orig", Verbs: clawapi.AllVerbs, Pods: []string{"trading-desk"}},
	}
	explicit := []pod.PodPrincipal{
		{Name: "sentinel", Verbs: clawapi.AllReadVerbs, Scope: "pod"},
	}
	result, err := mergePrincipals(auto, explicit, "trading-desk")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 principal after override, got %d", len(result))
	}
	// Explicit replaced auto — should only have read verbs now.
	for _, v := range clawapi.AllWriteVerbs {
		if result[0].Principal.AllowsVerb(v) {
			t.Fatalf("overridden principal should not have write verb %q", v)
		}
	}
	// Token should be a fresh one (explicit generates new token).
	if result[0].Principal.Token == "capi_orig" {
		t.Fatal("expected explicit override to generate a new token")
	}
}

func TestMergePrincipalsExplicitNewNameAppended(t *testing.T) {
	auto := []clawapi.Principal{
		{Name: "sentinel", Token: "capi_master", Verbs: clawapi.AllVerbs, Pods: []string{"trading-desk"}},
	}
	explicit := []pod.PodPrincipal{
		{Name: "dashboard", Verbs: []string{clawapi.VerbFleetStatus}, Scope: "pod"},
	}
	result, err := mergePrincipals(auto, explicit, "trading-desk")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 principals, got %d", len(result))
	}
}

func TestMergePrincipalsInjectIntoPreserved(t *testing.T) {
	explicit := []pod.PodPrincipal{
		{Name: "ci", Verbs: []string{clawapi.VerbFleetStatus}, Scope: "pod", InjectInto: "ci-runner"},
	}
	result, err := mergePrincipals(nil, explicit, "trading-desk")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].InjectInto != "ci-runner" {
		t.Fatalf("expected InjectInto=ci-runner, got %q", result[0].InjectInto)
	}
}

func TestMergePrincipalsInjectIntoCollisionFails(t *testing.T) {
	explicit := []pod.PodPrincipal{
		{Name: "alpha", Verbs: []string{clawapi.VerbFleetStatus}, Scope: "pod", InjectInto: "dashboard"},
		{Name: "beta", Verbs: []string{clawapi.VerbFleetLogs}, Scope: "pod", InjectInto: "dashboard"},
	}
	_, err := mergePrincipals(nil, explicit, "trading-desk")
	if err == nil {
		t.Fatal("expected inject-into collision error")
	}
	if !containsStr(err.Error(), "conflict") {
		t.Fatalf("expected 'conflict' in error, got: %v", err)
	}
}

func TestMergePrincipalsSameNameOverrideNotACollision(t *testing.T) {
	// Overriding an auto-generated principal by name is not a collision even if inject-into matches.
	auto := []clawapi.Principal{
		{Name: "sentinel", Token: "capi_orig", Verbs: clawapi.AllVerbs, Pods: []string{"pod"}},
	}
	explicit := []pod.PodPrincipal{
		{Name: "sentinel", Verbs: clawapi.AllReadVerbs, Scope: "pod", InjectInto: "sentinel-svc"},
	}
	_, err := mergePrincipals(auto, explicit, "pod")
	if err != nil {
		t.Fatalf("unexpected error on same-name override: %v", err)
	}
}

func TestMergePrincipalsScopeExpandsToPodName(t *testing.T) {
	explicit := []pod.PodPrincipal{
		{Name: "dash", Verbs: []string{clawapi.VerbFleetStatus}, Scope: "pod"},
	}
	result, err := mergePrincipals(nil, explicit, "my-pod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result[0].Principal.Pods) != 1 || result[0].Principal.Pods[0] != "my-pod" {
		t.Fatalf("expected Pods=[my-pod], got %v", result[0].Principal.Pods)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
