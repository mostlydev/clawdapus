//go:build spike

package main

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// Only generated synthetic/conformance pod copies opt out. Canonical examples
// retain conservative defaults, and no production flag or bypass is introduced.
func spikePermissiveExamplePolicy(t *testing.T, source string) string {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(source), &doc); err != nil {
		t.Fatal(err)
	}
	claw, _ := doc["x-claw"].(map[string]any)
	if claw == nil {
		claw = map[string]any{}
		doc["x-claw"] = claw
	}
	claw["data-policy-defaults"] = map[string]any{"regions": []string{"global"}, "training": "allowed", "retention": "provider-default", "local-retention": "allowed"}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
