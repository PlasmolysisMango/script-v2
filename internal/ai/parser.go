package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"jbs/internal/ai/llm"
)

var ErrInvalidAction = errors.New("invalid AI action")

// Decision is provider output, not an engine action. Thought is private memory
// only and is never included in submitted payloads or result diagnostics.
type Decision struct {
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload"`
	Thought string          `json:"thought"`
}

// ParseAction accepts a balanced JSON object, including a fenced JSON response.
// Exactly action, payload (an object), and thought (a string) are required.
func ParseAction(text string) (Decision, error) {
	if len(text) > llm.MaxOutputBytes {
		return Decision{}, ErrInvalidAction
	}
	obj, ok := balancedObject(text)
	if !ok {
		return Decision{}, ErrInvalidAction
	}
	dec := json.NewDecoder(strings.NewReader(obj))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return Decision{}, ErrInvalidAction
	}
	fields := map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return Decision{}, ErrInvalidAction
		}
		key, ok := tok.(string)
		if !ok || (key != "action" && key != "payload" && key != "thought") {
			return Decision{}, ErrInvalidAction
		}
		if _, duplicate := fields[key]; duplicate {
			return Decision{}, ErrInvalidAction
		}
		var raw json.RawMessage
		if dec.Decode(&raw) != nil {
			return Decision{}, ErrInvalidAction
		}
		fields[key] = raw
	}
	if _, err := dec.Token(); err != nil || len(fields) != 3 {
		return Decision{}, ErrInvalidAction
	}
	if _, err := dec.Token(); err != io.EOF {
		return Decision{}, ErrInvalidAction
	}
	var d Decision
	if json.Unmarshal(fields["action"], &d.Action) != nil || strings.TrimSpace(d.Action) == "" ||
		bytes.Equal(fields["thought"], []byte("null")) || json.Unmarshal(fields["thought"], &d.Thought) != nil {
		return Decision{}, ErrInvalidAction
	}
	if _, err := payloadObject(fields["payload"]); err != nil {
		return Decision{}, err
	}
	d.Payload = fields["payload"]
	return d, nil
}

func balancedObject(s string) (string, bool) {
	start, depth := -1, 0
	quoted, escaped := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if start < 0 {
			if c == '{' {
				start, depth = i, 1
			}
			continue
		}
		if quoted {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		switch c {
		case '"':
			quoted = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}

// Reject duplicate payload keys as well as non-object payloads.
func payloadObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, ErrInvalidAction
	}
	out := map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, ErrInvalidAction
		}
		key, ok := tok.(string)
		if !ok {
			return nil, ErrInvalidAction
		}
		if _, exists := out[key]; exists {
			return nil, ErrInvalidAction
		}
		var v json.RawMessage
		if dec.Decode(&v) != nil {
			return nil, ErrInvalidAction
		}
		out[key] = v
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, ErrInvalidAction
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, ErrInvalidAction
	}
	return out, nil
}
