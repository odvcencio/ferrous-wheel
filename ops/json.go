package ops

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// JSONPointer returns the value at an RFC 6901 JSON pointer. An empty pointer
// selects the full document. It rejects trailing JSON, missing values, and
// invalid array indexes. RawMessage preserves large integer values.
func JSONPointer(data []byte, pointer string) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var current json.RawMessage
	if err := decoder.Decode(&current); err != nil {
		return nil, fmt.Errorf("ops: decode JSON: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("ops: JSON has a second value")
		}
		return nil, fmt.Errorf("ops: trailing JSON: %w", err)
	}
	if pointer == "" {
		return current, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, errors.New("ops: JSON pointer must start with /")
	}
	for _, part := range strings.Split(pointer[1:], "/") {
		key, err := unescapePointerToken(part)
		if err != nil {
			return nil, err
		}
		trimmed := bytes.TrimSpace(current)
		if len(trimmed) == 0 {
			return nil, errors.New("ops: empty JSON value")
		}
		switch trimmed[0] {
		case '{':
			var object map[string]json.RawMessage
			if err := json.Unmarshal(current, &object); err != nil {
				return nil, err
			}
			value, exists := object[key]
			if !exists {
				return nil, fmt.Errorf("ops: JSON pointer key %q does not exist", key)
			}
			current = value
		case '[':
			var array []json.RawMessage
			if err := json.Unmarshal(current, &array); err != nil {
				return nil, err
			}
			if key == "" || (len(key) > 1 && key[0] == '0') || key[0] < '0' || key[0] > '9' {
				return nil, fmt.Errorf("ops: invalid JSON array index %q", key)
			}
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 || index >= len(array) {
				return nil, fmt.Errorf("ops: JSON array index %q is out of range", key)
			}
			current = array[index]
		default:
			return nil, fmt.Errorf("ops: JSON pointer cannot traverse scalar at %q", key)
		}
	}
	return current, nil
}

// DecodeJSONAt decodes the value at a JSON pointer into a Go type.
func DecodeJSONAt[T any](data []byte, pointer string) (T, error) {
	var value T
	raw, err := JSONPointer(data, pointer)
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return value, fmt.Errorf("ops: decode selected JSON: %w", err)
	}
	return value, nil
}

func unescapePointerToken(token string) (string, error) {
	var result strings.Builder
	for i := 0; i < len(token); i++ {
		if token[i] != '~' {
			result.WriteByte(token[i])
			continue
		}
		if i+1 == len(token) {
			return "", errors.New("ops: JSON pointer has an incomplete escape")
		}
		i++
		switch token[i] {
		case '0':
			result.WriteByte('~')
		case '1':
			result.WriteByte('/')
		default:
			return "", fmt.Errorf("ops: JSON pointer has invalid escape ~%c", token[i])
		}
	}
	return result.String(), nil
}
