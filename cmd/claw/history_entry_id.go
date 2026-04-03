package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const historyEntryIDPrefix = "hist1_"

type historyEntryEnvelope struct {
	ID             string `json:"id,omitempty"`
	TS             string `json:"ts"`
	RequestedModel string `json:"requested_model"`
}

// ensureHistoryEntryID mirrors cllama's stable history entry ID contract.
// When a legacy ledger line lacks an id field, it derives one from the raw JSON
// and returns an updated entry body suitable for replay/export.
func ensureHistoryEntryID(line []byte) ([]byte, historyEntryEnvelope, error) {
	trimmed := bytes.TrimSpace(line)
	var header historyEntryEnvelope
	if err := json.Unmarshal(trimmed, &header); err != nil {
		return nil, historyEntryEnvelope{}, fmt.Errorf("parse history entry: %w", err)
	}
	if strings.TrimSpace(header.ID) != "" {
		return append([]byte(nil), trimmed...), header, nil
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return nil, historyEntryEnvelope{}, fmt.Errorf("parse history entry object: %w", err)
	}
	header.ID = historyEntryIDFromJSON(trimmed)
	idRaw, err := json.Marshal(header.ID)
	if err != nil {
		return nil, historyEntryEnvelope{}, fmt.Errorf("marshal history entry id: %w", err)
	}
	obj["id"] = idRaw
	updated, err := json.Marshal(obj)
	if err != nil {
		return nil, historyEntryEnvelope{}, fmt.Errorf("marshal history entry: %w", err)
	}
	return updated, header, nil
}

func historyEntryIDFromJSON(raw []byte) string {
	sum := sha256.Sum256(bytes.TrimSpace(raw))
	return historyEntryIDPrefix + hex.EncodeToString(sum[:])
}
