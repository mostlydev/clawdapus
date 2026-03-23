package main

import (
	"fmt"
	"strings"

	"github.com/mostlydev/clawdapus/internal/clawapi"
	"github.com/mostlydev/clawdapus/internal/pod"
)

// mergedPrincipal pairs a resolved Principal with injection metadata.
type mergedPrincipal struct {
	Principal  clawapi.Principal
	InjectInto string // service name to receive CLAW_API_URL + CLAW_API_TOKEN; may be empty
}

// mergePrincipals combines auto-generated and explicit principals.
//
// Explicit principals with the same name as an auto-generated one replace it.
// New names are appended. If two distinct principals (not a same-name override)
// both target the same service via inject-into, an error is returned.
func mergePrincipals(auto []clawapi.Principal, explicit []pod.PodPrincipal, podName string) ([]mergedPrincipal, error) {
	// Build ordered list starting from auto-generated, keyed by name.
	nameToIdx := make(map[string]int, len(auto))
	result := make([]mergedPrincipal, 0, len(auto)+len(explicit))
	for _, p := range auto {
		nameToIdx[p.Name] = len(result)
		result = append(result, mergedPrincipal{Principal: p})
	}

	// Track inject-into targets: service → principal name that claimed it.
	injectTargets := make(map[string]string)
	// Seed with any auto-generated self principals that inject into their own service.
	// (Master injects into master service — track it too.)
	for _, m := range result {
		if len(m.Principal.Services) == 1 && m.InjectInto == "" {
			// Self principals inject into their own service by convention.
			// We don't conflict-check auto vs auto; only explicit collisions matter.
		}
	}

	for _, ep := range explicit {
		token, err := clawapi.GenerateToken()
		if err != nil {
			return nil, fmt.Errorf("principal %q: generate token: %w", ep.Name, err)
		}

		p := clawapi.Principal{
			Name:            ep.Name,
			Token:           token,
			Verbs:           append([]string{}, ep.Verbs...),
			Services:        append([]string{}, ep.Services...),
			ClawIDs:         append([]string{}, ep.ClawIDs...),
			ComposeServices: append([]string{}, ep.ComposeServices...),
		}
		if strings.TrimSpace(ep.Scope) == "pod" {
			p.Pods = []string{podName}
		}

		injectTarget := strings.TrimSpace(ep.InjectInto)
		if injectTarget != "" {
			// Check for collision: a different principal already claims this service.
			if existing, ok := injectTargets[injectTarget]; ok && existing != ep.Name {
				return nil, fmt.Errorf("inject-into conflict: both %q and %q target service %q", existing, ep.Name, injectTarget)
			}
			injectTargets[injectTarget] = ep.Name
		}

		merged := mergedPrincipal{Principal: p, InjectInto: injectTarget}

		if idx, exists := nameToIdx[ep.Name]; exists {
			// Same-name override: replace the auto-generated entry.
			result[idx] = merged
		} else {
			nameToIdx[ep.Name] = len(result)
			result = append(result, merged)
		}
	}

	return result, nil
}
