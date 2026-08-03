package health

import (
	"encoding/json"
	"fmt"
	"strings"
)

// HealthResult is the parsed output from a health probe.
type HealthResult struct {
	OK     bool
	Detail string
}

// openclawHealth is the JSON structure from `openclaw health --json`.
type openclawHealth struct {
	Status  string `json:"status"`
	Detail  string `json:"detail"`
	Version string `json:"version"`
}

// openclawReadiness is the JSON structure from the gateway's /readyz endpoint.
// Ready is a pointer so a response from an unrelated endpoint cannot be mistaken
// for a valid readiness result.
type openclawReadiness struct {
	Ready   *bool           `json:"ready"`
	Failing json.RawMessage `json:"failing"`
}

// ParseOpenClawReadinessJSON extracts readiness from the gateway's /readyz
// response. The endpoint does not require the runtime-generated gateway token.
func ParseOpenClawReadinessJSON(stdout []byte) (*HealthResult, error) {
	jsonBytes, err := firstJSONObject(stdout)
	if err != nil {
		return nil, err
	}

	var readiness openclawReadiness
	if err := json.Unmarshal(jsonBytes, &readiness); err != nil {
		return nil, fmt.Errorf("health probe: failed to parse readiness JSON: %w", err)
	}
	if readiness.Ready == nil {
		return nil, fmt.Errorf("health probe: readiness JSON has no ready field")
	}

	detail := "gateway ready"
	if !*readiness.Ready {
		detail = "gateway not ready"
		failing := strings.TrimSpace(string(readiness.Failing))
		if failing != "" && failing != "null" && failing != "[]" {
			detail += ": " + failing
		}
	}

	return &HealthResult{OK: *readiness.Ready, Detail: detail}, nil
}

// ParseHealthJSON extracts health status from stdout bytes.
// Handles leading noise by scanning for the first '{' character.
func ParseHealthJSON(stdout []byte) (*HealthResult, error) {
	jsonBytes, err := firstJSONObject(stdout)
	if err != nil {
		return nil, err
	}
	var h openclawHealth
	if err := json.Unmarshal(jsonBytes, &h); err != nil {
		return nil, fmt.Errorf("health probe: failed to parse JSON: %w", err)
	}

	return &HealthResult{
		OK:     h.Status == "ok",
		Detail: h.Detail,
	}, nil
}

func firstJSONObject(stdout []byte) ([]byte, error) {
	idx := strings.IndexByte(string(stdout), '{')
	if idx < 0 {
		return nil, fmt.Errorf("health probe: no JSON object found in output")
	}
	return stdout[idx:], nil
}
