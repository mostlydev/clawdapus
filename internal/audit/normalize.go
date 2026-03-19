package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

const maxScanTokenSize = 1024 * 1024

func NormalizeLine(line []byte) (*Event, error) {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, err
	}

	ts := strings.TrimSpace(stringField(raw, "timestamp"))
	if ts == "" {
		ts = strings.TrimSpace(stringField(raw, "ts"))
	}
	if ts == "" {
		return nil, fmt.Errorf("missing timestamp")
	}
	parsedTS, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return nil, fmt.Errorf("parse timestamp %q: %w", ts, err)
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
		SourceService: strings.TrimSpace(stringField(raw, "source_service")),
	}
	if value, ok := nullableStringField(raw, "intervention_reason"); ok {
		event.InterventionReason = strings.TrimSpace(value)
	} else if value, ok := nullableStringField(raw, "intervention"); ok {
		event.InterventionReason = strings.TrimSpace(value)
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
	if value, ok := float64Field(raw, "cost_usd"); ok {
		event.CostUSD = &value
	}

	return event, nil
}

func ParseReader(r io.Reader) ([]Event, int, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxScanTokenSize)

	events := make([]Event, 0)
	skipped := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		event, err := NormalizeLine([]byte(line))
		if err != nil {
			skipped++
			continue
		}
		events = append(events, *event)
	}
	if err := scanner.Err(); err != nil {
		return nil, skipped, err
	}
	return events, skipped, nil
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
