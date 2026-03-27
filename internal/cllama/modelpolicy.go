package cllama

import (
	"sort"
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

	slots := make([]string, 0, len(models))
	if ref := strings.TrimSpace(models["primary"]); ref != "" {
		slots = append(slots, "primary")
	}
	if ref := strings.TrimSpace(models["fallback"]); ref != "" {
		slots = append(slots, "fallback")
	}

	otherSlots := make([]string, 0, len(models))
	for slot, ref := range models {
		if slot == "primary" || slot == "fallback" || strings.TrimSpace(ref) == "" {
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
		allowed = append(allowed, AllowedModel{
			Slot: slot,
			Ref:  ref,
		})
	}
	return allowed
}
