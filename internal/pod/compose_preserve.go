package pod

import (
	"fmt"
	"strconv"
	"strings"
)

func deepCopyValue(v interface{}) interface{} {
	switch tv := v.(type) {
	case map[string]interface{}:
		return deepCopyMap(tv)
	case []interface{}:
		out := make([]interface{}, 0, len(tv))
		for _, item := range tv {
			out = append(out, deepCopyValue(item))
		}
		return out
	default:
		return v
	}
}

func deepCopyMap(in map[string]interface{}) map[string]interface{} {
	if len(in) == 0 {
		return make(map[string]interface{})
	}
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = deepCopyValue(v)
	}
	return out
}

func mapStringAny(raw interface{}) (map[string]interface{}, error) {
	if raw == nil {
		return nil, nil
	}
	switch tv := raw.(type) {
	case map[string]interface{}:
		return tv, nil
	default:
		return nil, fmt.Errorf("expected map, got %T", raw)
	}
}

func interfaceSlice(raw interface{}) ([]interface{}, error) {
	if raw == nil {
		return nil, nil
	}
	switch tv := raw.(type) {
	case []interface{}:
		return tv, nil
	case []string:
		out := make([]interface{}, 0, len(tv))
		for _, item := range tv {
			out = append(out, item)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected list, got %T", raw)
	}
}

func parseEnvironment(raw interface{}) (map[string]string, error) {
	if raw == nil {
		return nil, nil
	}

	switch tv := raw.(type) {
	case map[string]string:
		out := make(map[string]string, len(tv))
		for k, v := range tv {
			out[k] = v
		}
		return out, nil
	case map[string]interface{}:
		out := make(map[string]string, len(tv))
		for k, v := range tv {
			s, err := scalarToString(v)
			if err != nil {
				return nil, fmt.Errorf("key %q: %w", k, err)
			}
			out[k] = s
		}
		return out, nil
	case []string:
		out := make(map[string]string, len(tv))
		for i, item := range tv {
			key, value, err := parseEnvironmentEntry(item)
			if err != nil {
				return nil, fmt.Errorf("entry %d: %w", i, err)
			}
			out[key] = value
		}
		return out, nil
	case []interface{}:
		out := make(map[string]string, len(tv))
		for i, item := range tv {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("entry %d: expected string, got %T", i, item)
			}
			key, value, err := parseEnvironmentEntry(s)
			if err != nil {
				return nil, fmt.Errorf("entry %d: %w", i, err)
			}
			out[key] = value
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported environment value type %T", raw)
	}
}

func parseEnvironmentEntry(entry string) (string, string, error) {
	key := entry
	value := ""
	if idx := strings.Index(entry, "="); idx >= 0 {
		key = entry[:idx]
		value = entry[idx+1:]
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", fmt.Errorf("environment key must not be empty")
	}
	return key, value, nil
}

func scalarToString(v interface{}) (string, error) {
	switch tv := v.(type) {
	case nil:
		return "", nil
	case string:
		return tv, nil
	case int:
		return strconv.Itoa(tv), nil
	case int64:
		return strconv.FormatInt(tv, 10), nil
	case uint:
		return strconv.FormatUint(uint64(tv), 10), nil
	case uint64:
		return strconv.FormatUint(tv, 10), nil
	case float64:
		return strconv.FormatFloat(tv, 'f', -1, 64), nil
	case bool:
		if tv {
			return "true", nil
		}
		return "false", nil
	default:
		return "", fmt.Errorf("unsupported scalar type %T", v)
	}
}

func stringsToInterfaces(in []string) []interface{} {
	if len(in) == 0 {
		return nil
	}
	out := make([]interface{}, 0, len(in))
	for _, item := range in {
		out = append(out, item)
	}
	return out
}

func appendedSequence(raw interface{}, additions []interface{}) ([]interface{}, error) {
	if len(additions) == 0 {
		return interfaceSlice(raw)
	}

	existing, err := interfaceSlice(raw)
	if err != nil {
		return nil, err
	}

	out := make([]interface{}, 0, len(existing)+len(additions))
	out = append(out, existing...)
	for _, item := range additions {
		out = append(out, deepCopyValue(item))
	}
	return out, nil
}

func mergedEnvironment(base interface{}, layers ...map[string]string) (map[string]string, error) {
	out, err := parseEnvironment(base)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = make(map[string]string)
	}
	for _, layer := range layers {
		for k, v := range layer {
			out[k] = v
		}
	}
	return out, nil
}

func mergedLabels(base interface{}, additions map[string]string) (map[string]string, error) {
	out := make(map[string]string)

	switch tv := base.(type) {
	case nil:
	case map[string]string:
		for k, v := range tv {
			out[k] = v
		}
	case map[string]interface{}:
		for k, v := range tv {
			s, err := scalarToString(v)
			if err != nil {
				return nil, fmt.Errorf("key %q: %w", k, err)
			}
			out[k] = s
		}
	case []string:
		for i, item := range tv {
			key, value, err := parseEnvironmentEntry(item)
			if err != nil {
				return nil, fmt.Errorf("entry %d: %w", i, err)
			}
			out[key] = value
		}
	case []interface{}:
		for i, item := range tv {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("entry %d: expected string, got %T", i, item)
			}
			key, value, err := parseEnvironmentEntry(s)
			if err != nil {
				return nil, fmt.Errorf("entry %d: %w", i, err)
			}
			out[key] = value
		}
	default:
		return nil, fmt.Errorf("unsupported labels value type %T", base)
	}

	for k, v := range additions {
		out[k] = v
	}
	return out, nil
}

func mergedNetworks(base interface{}, network string) (interface{}, error) {
	switch tv := base.(type) {
	case nil:
		return []string{network}, nil
	case []string:
		out := append([]string(nil), tv...)
		for _, existing := range out {
			if existing == network {
				return out, nil
			}
		}
		return append(out, network), nil
	case []interface{}:
		out := make([]interface{}, 0, len(tv)+1)
		found := false
		for _, item := range tv {
			out = append(out, deepCopyValue(item))
			if s, ok := item.(string); ok && s == network {
				found = true
			}
		}
		if !found {
			out = append(out, network)
		}
		return out, nil
	case map[string]interface{}:
		out := deepCopyMap(tv)
		if _, ok := out[network]; !ok {
			out[network] = nil
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported networks value type %T", base)
	}
}

func mergedNamedMap(base interface{}, additions map[string]interface{}) (map[string]interface{}, error) {
	if len(additions) == 0 {
		return mapStringAny(base)
	}

	out := make(map[string]interface{})
	switch tv := base.(type) {
	case nil:
	case map[string]interface{}:
		out = deepCopyMap(tv)
	default:
		return nil, fmt.Errorf("expected map, got %T", base)
	}

	for k, v := range additions {
		if _, ok := out[k]; !ok {
			out[k] = deepCopyValue(v)
		}
	}
	return out, nil
}
