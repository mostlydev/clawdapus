package pod

import (
	"fmt"
	"sort"
)

// HostedCapabilityNames are the provider-hosted capabilities a service may
// be granted. They are the names cllama accepts in a compiled context. A
// remote MCP server is not among them: granting it would grant any
// destination.
var HostedCapabilityNames = []string{"code_interpreter", "file_search", "image_generation", "web_search"}

// parseHostedCapabilities validates x-claw.hosted-capabilities and returns
// the names sorted. A per-run service may not declare a grant in this
// version: per-run workers run with no hosted capability.
func parseHostedCapabilities(raw []string, lifecycle string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if lifecycle == LifecyclePerRun {
		return nil, fmt.Errorf("hosted-capabilities is not supported for a per-run service")
	}
	known := map[string]bool{}
	for _, name := range HostedCapabilityNames {
		known[name] = true
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(raw))
	for _, name := range raw {
		if !known[name] {
			return nil, fmt.Errorf("hosted-capabilities: %q is not a grantable capability (one of %v)", name, HostedCapabilityNames)
		}
		if seen[name] {
			return nil, fmt.Errorf("hosted-capabilities: %q is listed twice", name)
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}
