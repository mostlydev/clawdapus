package pod

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// fallbackOrdinalPattern matches the reserved normalized chain keys
// (fallback-2, fallback-3, ...). Authors declare chains with list form;
// the ordinal keys exist only as the internal normalized representation.
var fallbackOrdinalPattern = regexp.MustCompile(`^fallback-\d+$`)

// ModelSlots is the YAML surface for x-claw.models. Every slot takes a scalar
// provider/model ref; the fallback slot additionally accepts an ordered list,
// normalized to fallback, fallback-2, fallback-3, ... in declared order so the
// rest of the pipeline keeps operating on a flat slot map (ADR-019).
type ModelSlots map[string]string

func (m *ModelSlots) UnmarshalYAML(node *yaml.Node) error {
	if node.Tag == "!!null" {
		*m = nil
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("models: expected a map of slot -> provider/model")
	}

	out := make(map[string]string, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valueNode := node.Content[i+1]
		slot := strings.TrimSpace(keyNode.Value)

		if fallbackOrdinalPattern.MatchString(slot) {
			return fmt.Errorf("models: slot %q is reserved; declare fallback chains with list form (fallback: [ref, ref, ...])", slot)
		}
		if _, exists := out[slot]; exists {
			return fmt.Errorf("models: duplicate slot %q", slot)
		}

		switch valueNode.Kind {
		case yaml.ScalarNode:
			var ref string
			if err := valueNode.Decode(&ref); err != nil {
				return fmt.Errorf("models: slot %q: %w", slot, err)
			}
			out[slot] = ref
		case yaml.SequenceNode:
			if slot != "fallback" {
				return fmt.Errorf("models: slot %q does not accept a list; only fallback declares an ordered chain", slot)
			}
			refs, err := decodeFallbackChain(valueNode)
			if err != nil {
				return err
			}
			// Preserve an explicit empty chain as a tombstone. The compose-time
			// merge uses it to clear fallback refs inherited from pod defaults or
			// image labels, then removes the blank marker from resolved output.
			if len(refs) == 0 {
				out["fallback"] = ""
				continue
			}
			for idx, ref := range refs {
				out[fallbackSlotName(idx)] = ref
			}
		default:
			return fmt.Errorf("models: slot %q: expected a provider/model ref", slot)
		}
	}

	*m = out
	return nil
}

func decodeFallbackChain(node *yaml.Node) ([]string, error) {
	refs := make([]string, 0, len(node.Content))
	seen := make(map[string]struct{}, len(node.Content))
	for _, entry := range node.Content {
		var ref string
		if err := entry.Decode(&ref); err != nil {
			return nil, fmt.Errorf("models: fallback chain: %w", err)
		}
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return nil, fmt.Errorf("models: fallback chain must not contain blank entries")
		}
		if _, dup := seen[ref]; dup {
			return nil, fmt.Errorf("models: fallback chain declares %q twice", ref)
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}
	return refs, nil
}

func fallbackSlotName(index int) string {
	if index == 0 {
		return "fallback"
	}
	return fmt.Sprintf("fallback-%d", index+1)
}
