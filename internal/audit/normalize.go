package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

func NormalizeLine(line []byte) (*Event, error) {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, err
	}

	parsedTS, err := parseRawTimestamp(raw)
	if err != nil {
		return nil, err
	}

	eventType := strings.TrimSpace(stringField(raw, "type"))
	if eventType == "" {
		return nil, fmt.Errorf("missing type")
	}

	event := &Event{
		Timestamp:     parsedTS,
		ClawID:        strings.TrimSpace(stringField(raw, "claw_id")),
		Type:          eventType,
		Model:         strings.TrimSpace(stringField(raw, "model")),
		Error:         strings.TrimSpace(stringField(raw, "error")),
		FeedName:      strings.TrimSpace(stringField(raw, "feed_name")),
		FeedURL:       strings.TrimSpace(stringField(raw, "feed_url")),
		SourceService: strings.TrimSpace(stringField(raw, "source_service")),
		ChannelKind:   strings.TrimSpace(stringField(raw, "kind")),
		Status:        strings.TrimSpace(stringField(raw, "status")),
		ToolName:      strings.TrimSpace(stringField(raw, "tool_name")),
	}
	if event.SourceService == "" {
		event.SourceService = strings.TrimSpace(stringField(raw, "source"))
	}
	event.Channels = stringSliceField(raw, "channels")
	if value, ok := nullableStringField(raw, "intervention_reason"); ok {
		event.InterventionReason = strings.TrimSpace(value)
	} else if value, ok := nullableStringField(raw, "intervention"); ok {
		event.InterventionReason = strings.TrimSpace(value)
	}
	if value, ok := boolField(raw, "manifest_present"); ok {
		event.ManifestPresent = &value
	}
	if value, ok := intField(raw, "tools_count"); ok {
		event.ToolsCount = &value
	}
	if value, ok := int64Field(raw, "latency_ms"); ok {
		event.LatencyMS = &value
	}
	if value, ok := intField(raw, "status_code"); ok {
		event.StatusCode = &value
	}
	if value, ok := intField(raw, "tokens_in"); ok {
		event.TokensIn = &value
	}
	if value, ok := intField(raw, "tokens_out"); ok {
		event.TokensOut = &value
	}
	if value, ok := intField(raw, "retained"); ok {
		event.Retained = &value
	}
	if value, ok := intField(raw, "returned"); ok {
		event.Returned = &value
	}
	if value, ok := intField(raw, "omitted"); ok {
		event.Omitted = &value
	}
	if value, ok := intField(raw, "raw_bytes"); ok {
		event.RawBytes = &value
	}
	if value, ok := intField(raw, "digest_bytes"); ok {
		event.DigestBytes = &value
	}
	if value, ok := intField(raw, "digest_blocks"); ok {
		event.DigestBlocks = &value
	}
	if value, ok := intField(raw, "coverage_gaps"); ok {
		event.CoverageGaps = &value
	}
	if value, ok := boolField(raw, "deterministic_only"); ok {
		event.DeterministicOnly = &value
	}
	if value, ok := float64Field(raw, "cost_usd"); ok {
		event.CostUSD = &value
	}
	// provider_pool event fields
	event.Provider = strings.TrimSpace(stringField(raw, "provider"))
	event.KeyID = strings.TrimSpace(stringField(raw, "key_id"))
	event.Action = strings.TrimSpace(stringField(raw, "action"))
	event.Reason = strings.TrimSpace(stringField(raw, "reason"))
	event.CooldownUntil = strings.TrimSpace(stringField(raw, "cooldown_until"))

	return event, nil
}

func NormalizeSessionHistoryLine(line []byte) ([]Event, error) {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, err
	}

	toolTrace, ok := sliceField(raw, "tool_trace")
	if !ok || len(toolTrace) == 0 {
		return nil, nil
	}

	parsedTS, err := parseRawTimestamp(raw)
	if err != nil {
		return nil, err
	}

	model := strings.TrimSpace(stringField(raw, "effective_model"))
	if model == "" {
		model = strings.TrimSpace(stringField(raw, "requested_model"))
	}
	clawID := strings.TrimSpace(stringField(raw, "claw_id"))
	sessionEntryID := strings.TrimSpace(stringField(raw, "id"))
	finalStatus := strings.TrimSpace(stringField(raw, "status"))
	finalStatusCode, hasFinalStatusCode := intField(raw, "status_code")
	totalRounds, hasTotalRounds := 0, false
	if usage, ok := mapField(raw, "usage"); ok {
		totalRounds, hasTotalRounds = intField(usage, "total_rounds")
	}
	responseError := extractSessionHistoryResponseError(raw)

	events := make([]Event, 0)
	for _, roundValue := range toolTrace {
		roundMap, ok := roundValue.(map[string]any)
		if !ok {
			continue
		}
		roundNumber, hasRoundNumber := intField(roundMap, "round")
		toolCalls, ok := sliceField(roundMap, "tool_calls")
		if !ok || len(toolCalls) == 0 {
			continue
		}

		for _, callValue := range toolCalls {
			callMap, ok := callValue.(map[string]any)
			if !ok {
				continue
			}

			event := Event{
				Timestamp:      parsedTS,
				ClawID:         clawID,
				Type:           "tool_call",
				Model:          model,
				SourceService:  "session-history",
				SessionEntryID: sessionEntryID,
				FinalStatus:    finalStatus,
				ToolName:       strings.TrimSpace(stringField(callMap, "name")),
				ToolService:    strings.TrimSpace(stringField(callMap, "service")),
				ToolStatus:     strings.TrimSpace(stringField(callMap, "status")),
			}
			if latencyMS, ok := int64Field(callMap, "latency_ms"); ok {
				event.LatencyMS = &latencyMS
			}
			if statusCode, ok := intField(callMap, "status_code"); ok {
				event.StatusCode = &statusCode
			}
			if hasRoundNumber {
				value := roundNumber
				event.ToolRound = &value
			}
			if duplicate, _ := boolField(callMap, "duplicate"); duplicate {
				event.ToolDuplicate = true
			}
			if duplicateRound, ok := intField(callMap, "duplicate_of_round"); ok {
				value := duplicateRound
				event.ToolDuplicateRound = &value
			}
			if duplicateCount, ok := intField(callMap, "duplicate_count"); ok {
				value := duplicateCount
				event.ToolDuplicateCount = &value
			}
			if hasTotalRounds {
				value := totalRounds
				event.TotalRounds = &value
			}
			if hasFinalStatusCode {
				value := finalStatusCode
				event.FinalStatusCode = &value
			}
			if errText := extractToolResultError(callMap); errText != "" {
				event.Error = errText
			} else {
				event.Error = responseError
			}
			if event.ToolName == "" {
				continue
			}
			events = append(events, event)
		}
	}
	return events, nil
}

func ParseReader(r io.Reader) ([]Event, int, error) {
	br := bufio.NewReader(r)

	events := make([]Event, 0)
	skipped := 0
	for {
		chunk, readErr := br.ReadBytes('\n')
		if line := strings.TrimSpace(string(chunk)); line != "" {
			event, err := NormalizeLine([]byte(line))
			if err != nil {
				skipped++
			} else {
				events = append(events, *event)
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return events, skipped, nil
			}
			return events, skipped, readErr
		}
	}
}

func ParseSessionHistoryReader(r io.Reader) ([]Event, int, error) {
	br := bufio.NewReader(r)

	events := make([]Event, 0)
	skipped := 0
	for {
		chunk, readErr := br.ReadBytes('\n')
		if line := strings.TrimSpace(string(chunk)); line != "" {
			normalized, err := NormalizeSessionHistoryLine([]byte(line))
			if err != nil {
				skipped++
			} else {
				events = append(events, normalized...)
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return events, skipped, nil
			}
			return events, skipped, readErr
		}
	}
}

func parseRawTimestamp(raw map[string]any) (time.Time, error) {
	ts := strings.TrimSpace(stringField(raw, "timestamp"))
	if ts == "" {
		ts = strings.TrimSpace(stringField(raw, "ts"))
	}
	if ts == "" {
		return time.Time{}, fmt.Errorf("missing timestamp")
	}
	parsedTS, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse timestamp %q: %w", ts, err)
	}
	return parsedTS, nil
}

func stringField(raw map[string]any, key string) string {
	value, _ := raw[key].(string)
	return value
}

func nullableStringField(raw map[string]any, key string) (string, bool) {
	value, ok := raw[key]
	if !ok || value == nil {
		return "", false
	}
	s, ok := value.(string)
	return s, ok
}

func boolField(raw map[string]any, key string) (bool, bool) {
	value, ok := raw[key]
	if !ok || value == nil {
		return false, false
	}
	v, ok := value.(bool)
	return v, ok
}

func intField(raw map[string]any, key string) (int, bool) {
	value, ok := raw[key]
	if !ok || value == nil {
		return 0, false
	}
	switch v := value.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	}
	return 0, false
}

func int64Field(raw map[string]any, key string) (int64, bool) {
	value, ok := raw[key]
	if !ok || value == nil {
		return 0, false
	}
	switch v := value.(type) {
	case float64:
		return int64(v), true
	case int64:
		return v, true
	case int:
		return int64(v), true
	}
	return 0, false
}

func float64Field(raw map[string]any, key string) (float64, bool) {
	value, ok := raw[key]
	if !ok || value == nil {
		return 0, false
	}
	switch v := value.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	}
	return 0, false
}

func mapField(raw map[string]any, key string) (map[string]any, bool) {
	value, ok := raw[key]
	if !ok || value == nil {
		return nil, false
	}
	m, ok := value.(map[string]any)
	return m, ok
}

func sliceField(raw map[string]any, key string) ([]any, bool) {
	value, ok := raw[key]
	if !ok || value == nil {
		return nil, false
	}
	s, ok := value.([]any)
	return s, ok
}

func stringSliceField(raw map[string]any, key string) []string {
	values, ok := sliceField(raw, key)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		item, ok := value.(string)
		if !ok {
			continue
		}
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func extractToolResultError(call map[string]any) string {
	result, ok := mapField(call, "result")
	if !ok {
		return ""
	}
	if errText, ok := nullableStringField(result, "error"); ok {
		return strings.TrimSpace(errText)
	}
	if errObj, ok := mapField(result, "error"); ok {
		if message, ok := nullableStringField(errObj, "message"); ok {
			return strings.TrimSpace(message)
		}
	}
	if message, ok := nullableStringField(result, "message"); ok {
		return strings.TrimSpace(message)
	}
	return ""
}

func extractSessionHistoryResponseError(raw map[string]any) string {
	response, ok := mapField(raw, "response")
	if !ok {
		return ""
	}
	payload, ok := mapField(response, "json")
	if !ok {
		return ""
	}
	if errText, ok := nullableStringField(payload, "error"); ok {
		return strings.TrimSpace(errText)
	}
	if errObj, ok := mapField(payload, "error"); ok {
		if message, ok := nullableStringField(errObj, "message"); ok {
			return strings.TrimSpace(message)
		}
	}
	if message, ok := nullableStringField(payload, "message"); ok {
		return strings.TrimSpace(message)
	}
	return ""
}
