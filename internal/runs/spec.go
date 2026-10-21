package runs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// APIError is a client-visible error. Messages never carry secrets or spec
// content.
type APIError struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

func apiErr(status int, code, format string, args ...any) *APIError {
	return &APIError{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

var specFields = map[string]string{
	"template":            "string",
	"task":                "string",
	"tools":               "strings",
	"max_runtime_seconds": "integer",
}

// CanonicalSpec validates a raw launch spec and returns its canonical JSON
// form (sorted keys, no insignificant whitespace, no HTML escaping), the
// parsed Spec, and the digest "sha256:<hex>" over the canonical bytes.
// Only strings, integers and string arrays are accepted, so the canonical
// form is unambiguous across languages.
func CanonicalSpec(raw json.RawMessage) ([]byte, *Spec, string, error) {
	if !utf8.Valid(raw) {
		return nil, nil, "", apiErr(http.StatusBadRequest, "spec_invalid", "spec must be valid UTF-8")
	}
	if err := rejectLoneSurrogates(raw); err != nil {
		return nil, nil, "", err
	}
	if err := rejectDuplicateKeys(raw); err != nil {
		return nil, nil, "", err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var generic map[string]any
	if err := dec.Decode(&generic); err != nil || generic == nil {
		return nil, nil, "", apiErr(http.StatusBadRequest, "spec_invalid", "spec must be a JSON object")
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, nil, "", apiErr(http.StatusBadRequest, "spec_invalid", "spec must be a single JSON object")
	}
	keys := make([]string, 0, len(generic))
	for k := range generic {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		kind, ok := specFields[k]
		if !ok {
			return nil, nil, "", apiErr(http.StatusBadRequest, "spec_invalid", "spec contains an unknown field")
		}
		v := generic[k]
		switch kind {
		case "string":
			if _, ok := v.(string); !ok {
				return nil, nil, "", apiErr(http.StatusBadRequest, "spec_invalid", "spec.%s must be a string", k)
			}
		case "strings":
			arr, ok := v.([]any)
			if !ok {
				return nil, nil, "", apiErr(http.StatusBadRequest, "spec_invalid", "spec.%s must be an array of strings", k)
			}
			for _, item := range arr {
				if _, ok := item.(string); !ok {
					return nil, nil, "", apiErr(http.StatusBadRequest, "spec_invalid", "spec.%s must be an array of strings", k)
				}
			}
		case "integer":
			n, ok := v.(json.Number)
			if !ok || strings.ContainsAny(n.String(), ".eE") {
				return nil, nil, "", apiErr(http.StatusBadRequest, "spec_invalid", "spec.%s must be an integer", k)
			}
		}
	}
	canonical, err := encodeCanonical(generic, keys)
	if err != nil {
		return nil, nil, "", err
	}
	var spec Spec
	if err := json.Unmarshal(canonical, &spec); err != nil {
		return nil, nil, "", apiErr(http.StatusBadRequest, "spec_invalid", "spec does not match the v1 schema")
	}
	if spec.Tools == nil {
		spec.Tools = []string{}
	}
	sum := sha256.Sum256(canonical)
	return canonical, &spec, "sha256:" + hex.EncodeToString(sum[:]), nil
}

// rejectDuplicateKeys walks the token stream so a spec with a repeated key is
// refused: decoders disagree on which duplicate wins, and so would digests.
func rejectDuplicateKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var walk func() error
	walk = func() error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tok {
		case json.Delim('{'):
			seen := map[string]struct{}{}
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return err
				}
				key, _ := keyTok.(string)
				if _, dup := seen[key]; dup {
					return errDuplicateKey
				}
				seen[key] = struct{}{}
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		case json.Delim('['):
			for dec.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		}
		return nil
	}
	if err := walk(); err != nil {
		if err == errDuplicateKey {
			return apiErr(http.StatusBadRequest, "spec_invalid", "spec contains a duplicate key")
		}
		return apiErr(http.StatusBadRequest, "spec_invalid", "spec must be a JSON object")
	}
	return nil
}

var errDuplicateKey = errors.New("duplicate key")

// encodeCanonical emits the RFC 8785 (JCS) form of a spec restricted to
// strings, int64 integers and string arrays. Keys are ASCII here, so sorting
// by bytes equals JCS's UTF-16 code-unit order. Strings escape only '"', '\\'
// and C0 controls; every other rune, including U+2028/U+2029, is literal.
func encodeCanonical(v map[string]any, keys []string) ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		if err := writeJCSString(&b, k); err != nil {
			return nil, err
		}
		b.WriteByte(':')
		switch val := v[k].(type) {
		case string:
			if err := writeJCSString(&b, val); err != nil {
				return nil, err
			}
		case json.Number:
			n, err := strconv.ParseInt(val.String(), 10, 64)
			if err != nil || n > maxExactInteger || n < -maxExactInteger {
				return nil, apiErr(http.StatusBadRequest, "spec_invalid", "spec integers must be within ±(2^53-1)")
			}
			b.WriteString(strconv.FormatInt(n, 10))
		case []any:
			b.WriteByte('[')
			for j, item := range val {
				if j > 0 {
					b.WriteByte(',')
				}
				if err := writeJCSString(&b, item.(string)); err != nil {
					return nil, err
				}
			}
			b.WriteByte(']')
		}
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func writeJCSString(b *bytes.Buffer, s string) error {
	if !utf8.ValidString(s) {
		return apiErr(http.StatusBadRequest, "spec_invalid", "spec strings must be valid UTF-8")
	}
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return nil
}

// maxExactInteger is the largest integer JCS serializes exactly.
const maxExactInteger = 1<<53 - 1

// rejectLoneSurrogates refuses \u escapes that do not form valid UTF-16
// pairs. encoding/json would silently replace them with U+FFFD, and the
// canonical form would then disagree with the sender's.
func rejectLoneSurrogates(raw []byte) error {
	bad := apiErr(http.StatusBadRequest, "spec_invalid", "spec strings must be valid Unicode")
	inString := false
	pendingHigh := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			continue
		}
		if c == '"' {
			if pendingHigh {
				return bad
			}
			inString = false
			continue
		}
		if c != '\\' {
			if pendingHigh {
				return bad
			}
			continue
		}
		if i+1 >= len(raw) {
			return bad
		}
		if raw[i+1] != 'u' {
			if pendingHigh {
				return bad
			}
			i++
			continue
		}
		if i+5 >= len(raw) {
			return bad
		}
		code, err := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
		if err != nil {
			return bad
		}
		i += 5
		switch {
		case code >= 0xD800 && code <= 0xDBFF:
			if pendingHigh {
				return bad
			}
			pendingHigh = true
		case code >= 0xDC00 && code <= 0xDFFF:
			if !pendingHigh {
				return bad
			}
			pendingHigh = false
		default:
			if pendingHigh {
				return bad
			}
		}
	}
	return nil
}
