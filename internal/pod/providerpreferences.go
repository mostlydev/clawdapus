package pod

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mostlydev/clawdapus/internal/driver"
)

type rawProviderPreferences struct {
	Only              []string `yaml:"only"`
	ZDR               *bool    `yaml:"zdr"`
	AllowFallbacks    *bool    `yaml:"allow_fallbacks"`
	RequireParameters *bool    `yaml:"require_parameters"`
	DataCollection    *string  `yaml:"data_collection"`
}

// The worker sends this object to OpenRouter verbatim, so any key outside the
// narrowing subset is refused rather than ignored.
func (r *rawProviderPreferences) UnmarshalYAML(node *yaml.Node) error {
	if err := rejectUnknownKeys(node, "provider-preferences slot", "only", "zdr", "allow_fallbacks", "require_parameters", "data_collection"); err != nil {
		return err
	}
	type alias rawProviderPreferences
	return node.Decode((*alias)(r))
}

// OpenRouter endpoint slugs: provider[/variant][/region][/tier].
var openRouterSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*(/[a-z0-9][a-z0-9._-]*){0,3}$`)

// parseProviderPreferences validates x-claw.provider-preferences. Only per-run
// workers compile it; the openrouter/* model check runs at compile time
// against the merged models.
func parseProviderPreferences(raw map[string]*rawProviderPreferences, lifecycle string) (map[string]*driver.OpenRouterPreferences, error) {
	if raw == nil {
		return nil, nil
	}
	if lifecycle != LifecyclePerRun {
		return nil, fmt.Errorf("x-claw.provider-preferences requires x-claw.lifecycle: %s", LifecyclePerRun)
	}
	slots := make([]string, 0, len(raw))
	for slot := range raw {
		slots = append(slots, slot)
	}
	sort.Strings(slots)
	out := make(map[string]*driver.OpenRouterPreferences, len(raw))
	for _, rawSlot := range slots {
		slot := strings.TrimSpace(rawSlot)
		if slot == "" {
			return nil, fmt.Errorf("x-claw.provider-preferences: empty model slot")
		}
		if _, dup := out[slot]; dup {
			return nil, fmt.Errorf("x-claw.provider-preferences: model slot %q is declared more than once", slot)
		}
		prefs, err := parseOpenRouterPreferences(raw[rawSlot])
		if err != nil {
			return nil, fmt.Errorf("x-claw.provider-preferences.%s: %w", slot, err)
		}
		out[slot] = prefs
	}
	return out, nil
}

func parseOpenRouterPreferences(raw *rawProviderPreferences) (*driver.OpenRouterPreferences, error) {
	if raw == nil {
		return nil, fmt.Errorf("must declare at least one preference")
	}
	prefs := &driver.OpenRouterPreferences{ZDR: raw.ZDR, AllowFallbacks: raw.AllowFallbacks, RequireParameters: raw.RequireParameters}
	if raw.Only != nil {
		if len(raw.Only) == 0 {
			return nil, fmt.Errorf("only must list at least one endpoint")
		}
		seen := map[string]bool{}
		for _, slug := range raw.Only {
			slug = strings.TrimSpace(slug)
			if !openRouterSlugPattern.MatchString(slug) {
				return nil, fmt.Errorf("only: %q is not an OpenRouter endpoint slug (provider[/variant][/region][/tier])", slug)
			}
			if seen[slug] {
				return nil, fmt.Errorf("only: %q is listed more than once", slug)
			}
			seen[slug] = true
			prefs.Only = append(prefs.Only, slug)
		}
	}
	if raw.ZDR != nil && !*raw.ZDR {
		return nil, fmt.Errorf("zdr may only be true")
	}
	if raw.AllowFallbacks != nil && *raw.AllowFallbacks {
		return nil, fmt.Errorf("allow_fallbacks may only be false")
	}
	if raw.RequireParameters != nil && !*raw.RequireParameters {
		return nil, fmt.Errorf("require_parameters may only be true")
	}
	if raw.DataCollection != nil {
		if *raw.DataCollection != "deny" {
			return nil, fmt.Errorf("data_collection may only be deny")
		}
		prefs.DataCollection = "deny"
	}
	if len(prefs.Object()) == 0 {
		return nil, fmt.Errorf("must declare at least one preference")
	}
	return prefs, nil
}
