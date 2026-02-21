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

// ParseHealthJSON extracts health status from stdout bytes.
// Handles leading noise by scanning for the first '{' character.
func ParseHealthJSON(stdout []byte) (*HealthResult, error) {
	s := string(stdout)

	idx := strings.Index(s, "{")
	if idx < 0 {
		return nil, fmt.Errorf("health probe: no JSON object found in output")
	}

	jsonStr := s[idx:]
	var h openclawHealth
	if err := json.Unmarshal([]byte(jsonStr), &h); err != nil {
		return nil, fmt.Errorf("health probe: failed to parse JSON: %w", err)
	}

	return &HealthResult{
		OK:     h.Status == "ok",
		Detail: h.Detail,
	}, nil
}
