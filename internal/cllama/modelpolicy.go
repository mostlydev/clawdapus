package cllama

import (
	"sort"
	"strconv"
	"strings"
)

const ModelPolicyModeClamp = "clamp"

type AllowedModel struct {
	Slot string `json:"slot"`
	Ref  string `json:"ref"`
}

type ModelPolicy struct {
	Mode    string         `json:"mode"`
	Allowed []AllowedModel `json:"allowed"`
}

// CompileModelPolicy builds a deterministic per-agent model policy from
// resolved MODEL directives. Empty refs are ignored. When no valid refs remain,
// nil is returned and no policy should be emitted.
func CompileModelPolicy(models map[string]string) *ModelPolicy {
	allowed := orderedAllowedModels(models)
	if len(allowed) == 0 {
		return nil
	}
	return &ModelPolicy{
		Mode:    ModelPolicyModeClamp,
		Allowed: allowed,
	}
}

// InjectCompiledModelPolicy clones meta and attaches a compiled model_policy
// block when the agent declares any MODEL refs.
func InjectCompiledModelPolicy(meta map[string]any, models map[string]string) map[string]any {
	out := make(map[string]any, len(meta)+1)
	for k, v := range meta {
		out[k] = v
	}
	if policy := CompileModelPolicy(models); policy != nil {
		out["model_policy"] = policy
	}
	return out
}

func orderedAllowedModels(models map[string]string) []AllowedModel {
	if len(models) == 0 {
		return nil
	}

	// Slot keys in policy order: primary, the fallback chain (fallback,
	// fallback-2, ... in ordinal order), then remaining slots sorted by name.
	// Every chain link is emitted with slot name "fallback" because cllama's
	// failover walks each fallback-slot entry in declared order.
	slots := make([]string, 0, len(models))
	if ref := strings.TrimSpace(models["primary"]); ref != "" {
		slots = append(slots, "primary")
	}
	slots = append(slots, orderedFallbackSlots(models)...)

	otherSlots := make([]string, 0, len(models))
	for slot, ref := range models {
		if slot == "primary" || FallbackSlotOrdinal(slot) > 0 || strings.TrimSpace(ref) == "" {
			continue
		}
		otherSlots = append(otherSlots, slot)
	}
	sort.Strings(otherSlots)
	slots = append(slots, otherSlots...)

	seen := make(map[string]struct{}, len(slots))
	allowed := make([]AllowedModel, 0, len(slots))
	for _, slot := range slots {
		ref := strings.TrimSpace(models[slot])
		if ref == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		name := slot
		if FallbackSlotOrdinal(slot) > 0 {
			name = "fallback"
		}
		allowed = append(allowed, AllowedModel{
			Slot: name,
			Ref:  ref,
		})
	}
	return allowed
}

// FallbackSlotOrdinal returns the 1-based chain position for fallback-family
// slot keys ("fallback" -> 1, "fallback-2" -> 2, ...) and 0 for other slots.
func FallbackSlotOrdinal(slot string) int {
	if slot == "fallback" {
		return 1
	}
	rest, ok := strings.CutPrefix(slot, "fallback-")
	if !ok || rest == "" {
		return 0
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 2 || strconv.Itoa(n) != rest {
		return 0
	}
	return n
}

// FallbackChain returns the declared fallback refs in chain order (fallback,
// fallback-2, ...), with blanks skipped.
func FallbackChain(models map[string]string) []string {
	slots := orderedFallbackSlots(models)
	chain := make([]string, 0, len(slots))
	for _, slot := range slots {
		if ref := strings.TrimSpace(models[slot]); ref != "" {
			chain = append(chain, ref)
		}
	}
	return chain
}

func orderedFallbackSlots(models map[string]string) []string {
	family := make([]string, 0, 2)
	for slot := range models {
		if FallbackSlotOrdinal(slot) > 0 {
			family = append(family, slot)
		}
	}
	sort.Slice(family, func(i, j int) bool {
		return FallbackSlotOrdinal(family[i]) < FallbackSlotOrdinal(family[j])
	})
	return family
}
