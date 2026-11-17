package ai

import (
	"encoding/json"
	"slices"
	"strings"
	"unicode/utf8"

	"jbs/internal/engine"
)

type location struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Remaining int    `json:"remaining"`
}

type voteQuestion struct {
	ID      string          `json:"id"`
	Options []engine.Option `json:"options"`
}

type voteView struct {
	Questions []voteQuestion    `json:"questions"`
	Cast      map[string]string `json:"cast"`
}

func votes(v engine.View) voteView {
	b, _ := json.Marshal(v.Data["votes"])
	var out voteView
	_ = json.Unmarshal(b, &out)
	return out
}

func firstVote(s engine.ActionSpec, v engine.View) (question, option string) {
	vv := votes(v)
	for _, allowed := range s.Options {
		if _, cast := vv.Cast[allowed.Value]; cast {
			continue
		}
		for _, q := range vv.Questions {
			if q.ID == allowed.Value && len(q.Options) > 0 {
				return q.ID, q.Options[0].Value
			}
		}
	}
	return "", ""
}

type clueView struct {
	Own       json.RawMessage `json:"own"`
	Public    json.RawMessage `json:"public"`
	Locations []location      `json:"locations"`
	AP        int             `json:"ap"`
}

func clues(v engine.View) (clueView, bool) {
	data, ok := v.Data["clues"]
	if !ok {
		return clueView{}, false
	}
	b, err := json.Marshal(data)
	var c clueView
	if err != nil || json.Unmarshal(b, &c) != nil {
		return clueView{}, false
	}
	return c, true
}

// Only compulsory progress actions drive the loop, never optional PM/publish.
// Search takes priority over ready while action points/locations remain.
func turnActions(v engine.View) []engine.ActionSpec {
	var out []engine.ActionSpec
	for _, s := range v.Allowed {
		if s.Optional {
			continue
		}
		if s.Type != "host.advance" {
			out = append(out, s)
		}
	}
	if v.Phase.Type == "reading" || v.Phase.Type == "ready_check" {
		if s, ok := findSpec(out, "ready"); ok {
			return []engine.ActionSpec{s}
		}
	}
	if s, ok := findSpec(out, "search"); ok {
		c, hasClues := clues(v)
		if !hasClues || (c.AP > 0 && slices.ContainsFunc(c.Locations, func(l location) bool { return l.Remaining > 0 })) {
			return []engine.ActionSpec{s}
		}
		out = slices.DeleteFunc(out, func(s engine.ActionSpec) bool { return s.Type == "search" })
	}
	if s, ok := findSpec(out, "ready"); ok {
		return []engine.ActionSpec{s}
	}
	return out
}

func findSpec(specs []engine.ActionSpec, typ string) (engine.ActionSpec, bool) {
	for _, s := range specs {
		if s.Type == typ {
			return s, true
		}
	}
	return engine.ActionSpec{}, false
}

func payloadKeys(s engine.ActionSpec) []string {
	var keys []string
	for k := range s.Params {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if len(keys) != 0 {
		return keys
	}
	switch s.Type {
	case "say":
		return []string{"text"}
	case "search":
		return []string{"location"}
	case "vote":
		return []string{"option"}
	case "vote.cast":
		return []string{"option", "question"}
	default:
		return nil
	}
}

func actionSchema(specs []engine.ActionSpec) map[string]any {
	schema := map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema"}
	if len(specs) == 0 {
		// An empty oneOf is not a valid JSON Schema; not {} rejects every value.
		schema["not"] = map[string]any{}
		return schema
	}
	branches := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		keys := append([]string{}, payloadKeys(spec)...)
		properties := make(map[string]any, len(keys))
		for _, key := range keys {
			properties[key] = map[string]any{"type": "string", "minLength": 1}
		}
		branches = append(branches, map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{"type": "string", "const": spec.Type},
				"payload": map[string]any{
					"type": "object", "properties": properties,
					"required": keys, "additionalProperties": false,
				},
				"thought": map[string]any{"type": "string"},
			},
			"required": []string{"action", "payload", "thought"}, "additionalProperties": false,
		})
	}
	schema["oneOf"] = branches
	return schema
}

func validateDecision(d Decision, v engine.View) error {
	s, ok := findSpec(turnActions(v), d.Action)
	if !ok {
		return ErrInvalidAction
	}
	p, err := payloadObject(d.Payload)
	if err != nil {
		return err
	}
	keys := payloadKeys(s)
	if len(p) != len(keys) {
		return ErrInvalidAction
	}
	for k, raw := range p {
		if !slices.Contains(keys, k) || k == "thought" {
			return ErrInvalidAction
		}
		var value string
		if json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) == "" {
			return ErrInvalidAction
		}
		switch d.Action {
		case "say":
			if utf8.RuneCountInString(value) > 4000 {
				return ErrInvalidAction
			}
		case "search":
			if c, exists := clues(v); exists {
				if c.AP <= 0 || !slices.ContainsFunc(c.Locations, func(l location) bool { return l.ID == value && l.Remaining > 0 }) {
					return ErrInvalidAction
				}
			} else if len(s.Options) == 0 {
				return ErrInvalidAction
			}
			if len(s.Options) > 0 && !slices.ContainsFunc(s.Options, func(o engine.Option) bool { return o.Value == value }) {
				return ErrInvalidAction
			}
		case "vote":
			if !slices.ContainsFunc(s.Options, func(o engine.Option) bool { return o.Value == value }) {
				return ErrInvalidAction
			}
		}
	}
	if d.Action == "vote.cast" {
		var question, option string
		_ = json.Unmarshal(p["question"], &question)
		_ = json.Unmarshal(p["option"], &option)
		vv := votes(v)
		if _, cast := vv.Cast[question]; cast {
			return ErrInvalidAction
		}
		if !slices.ContainsFunc(s.Options, func(o engine.Option) bool { return o.Value == question }) {
			return ErrInvalidAction
		}
		if !slices.ContainsFunc(vv.Questions, func(q voteQuestion) bool {
			return q.ID == question && slices.ContainsFunc(q.Options, func(o engine.Option) bool { return o.Value == option })
		}) {
			return ErrInvalidAction
		}
	}
	return nil
}

func safeDecision(v engine.View, restrict string) (Decision, bool) {
	specs := turnActions(v)
	// Passing is safer than fabricating a speech when the provider fails.
	for _, typ := range []string{"ready", "pass", "search", "vote.cast", "vote", "say"} {
		if restrict != "" && typ != restrict {
			continue
		}
		s, ok := findSpec(specs, typ)
		if !ok {
			continue
		}
		payload := map[string]string{}
		var value string
		switch typ {
		case "say":
			value = "我想先听听大家对现有线索的看法。"
		case "search":
			if c, exists := clues(v); exists {
				for _, l := range c.Locations {
					if l.Remaining > 0 && (len(s.Options) == 0 || slices.ContainsFunc(s.Options, func(o engine.Option) bool { return o.Value == l.ID })) {
						value = l.ID
						break
					}
				}
			} else if len(s.Options) > 0 {
				value = s.Options[0].Value
			}
		case "vote":
			if len(s.Options) > 0 {
				value = s.Options[0].Value
			}
		}
		if typ == "vote.cast" {
			question, option := firstVote(s, v)
			payload["question"], payload["option"] = question, option
		} else {
			for _, k := range payloadKeys(s) {
				payload[k] = value
			}
		}
		b, _ := json.Marshal(payload)
		d := Decision{Action: typ, Payload: b}
		if validateDecision(d, v) == nil {
			return d, true
		}
	}
	return Decision{}, false
}
