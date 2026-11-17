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

type poolItem struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Kind   string `json:"kind"`
	Number int    `json:"number"`
}

type clueView struct {
	Own       json.RawMessage `json:"own"`
	Public    json.RawMessage `json:"public"`
	Locations []location      `json:"locations"`
	Pool      []poolItem      `json:"pool"`
	AP        int             `json:"ap"`
	Quota     int             `json:"quota"`
	PicksLeft int             `json:"picks_left"`
	Mode      string          `json:"mode"`
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

type cardView struct {
	Playable []struct {
		ID    string `json:"id"`
		Kind  string `json:"kind"`
		Color string `json:"color"`
	} `json:"playable"`
}

type diceView struct {
	Question string            `json:"question"`
	Keywords map[string]string `json:"keywords"`
}

// Only compulsory progress actions drive the loop, never optional PM/publish,
// strategy skills, or host-only controls.
// Search takes priority over ready while action points/locations remain.
func turnActions(v engine.View) []engine.ActionSpec {
	var out []engine.ActionSpec
	for _, s := range v.Allowed {
		if s.Optional || s.Type == "points.skill" {
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
		remaining := slices.ContainsFunc(c.Locations, func(l location) bool { return l.Remaining > 0 })
		if !hasClues || (c.AP > 0 && (remaining || len(c.Pool) > 0)) {
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
	if len(s.Fields) > 0 {
		for _, f := range s.Fields {
			keys = append(keys, f.Key)
		}
	} else {
		for k := range s.Params {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	if len(keys) != 0 {
		return keys
	}
	switch s.Type {
	case "say":
		return []string{"text"}
	case "search":
		// A bare search spec is the legacy location search; pool searches
		// without a kind/pick parameter are parameterless random draws.
		if len(s.Options) > 0 {
			return []string{"location"}
		}
		return nil
	case "vote":
		return []string{"option"}
	case "vote.cast":
		return []string{"option", "question"}
	default:
		return nil
	}
}

func requiredKeys(s engine.ActionSpec) []string {
	if len(s.Fields) == 0 {
		return payloadKeys(s)
	}
	var keys []string
	for _, f := range s.Fields {
		if f.Required {
			keys = append(keys, f.Key)
		}
	}
	slices.Sort(keys)
	return keys
}

func fieldOptions(s engine.ActionSpec, key string) []engine.Option {
	for _, f := range s.Fields {
		if f.Key == key {
			return f.Options
		}
	}
	if len(s.Fields) == 0 && len(payloadKeys(s)) == 1 && len(s.Options) > 0 {
		return s.Options
	}
	return nil
}

func schemaFieldOptions(s engine.ActionSpec, key string) []engine.Option {
	for _, f := range s.Fields {
		if f.Key == key {
			return f.Options
		}
	}
	return nil
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
		keys := payloadKeys(spec)
		properties := make(map[string]any, len(keys))
		for _, key := range keys {
			prop := map[string]any{"type": "string", "minLength": 1}
			if opts := schemaFieldOptions(spec, key); len(opts) > 0 {
				values := make([]string, 0, len(opts))
				for _, o := range opts {
					values = append(values, o.Value)
				}
				prop["enum"] = values
			}
			properties[key] = prop
		}
		required := requiredKeys(spec)
		if required == nil {
			required = []string{}
		}
		branches = append(branches, map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{"type": "string", "const": spec.Type},
				"payload": map[string]any{
					"type": "object", "properties": properties,
					"required": required, "additionalProperties": false,
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
	if len(s.Fields) == 0 {
		if len(p) != len(keys) {
			return ErrInvalidAction
		}
	} else if len(p) > len(keys) {
		return ErrInvalidAction
	}
	for _, k := range requiredKeys(s) {
		if _, ok := p[k]; !ok {
			return ErrInvalidAction
		}
	}
	for k, raw := range p {
		if !slices.Contains(keys, k) || k == "thought" {
			return ErrInvalidAction
		}
		var value string
		if json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) == "" {
			return ErrInvalidAction
		}
		if opts := fieldOptions(s, k); len(opts) > 0 &&
			!slices.ContainsFunc(opts, func(o engine.Option) bool { return o.Value == value }) {
			return ErrInvalidAction
		}
		switch d.Action {
		case "say":
			if utf8.RuneCountInString(value) > 4000 {
				return ErrInvalidAction
			}
		case "search":
			if k == "location" {
				if c, exists := clues(v); exists {
					if c.AP <= 0 || !slices.ContainsFunc(c.Locations, func(l location) bool { return l.ID == value && l.Remaining > 0 }) {
						return ErrInvalidAction
					}
				} else if len(s.Options) == 0 {
					return ErrInvalidAction
				}
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
	for _, typ := range []string{"ready", "pass", "dice.ask", "dice.answer", "card.play", "card.draw", "card.pass", "search", "vote.cast", "vote", "say"} {
		if restrict != "" && typ != restrict {
			continue
		}
		s, ok := findSpec(specs, typ)
		if !ok {
			continue
		}
		payload := map[string]string{}
		var value string
		optionalKind := false
		switch typ {
		case "say":
			value = "我想先听听大家对现有线索的看法。"
		case "dice.answer":
			var dv diceView
			b, _ := json.Marshal(v.Data["dice"])
			_ = json.Unmarshal(b, &dv)
			for trigger, detail := range dv.Keywords {
				if strings.Contains(dv.Question, trigger) && detail != "" {
					value = detail
					break
				}
			}
			if value == "" {
				value = "我确认上述内容属实，并愿意继续说明。"
			}
		case "card.play":
			var cv cardView
			b, _ := json.Marshal(v.Data["cards"])
			_ = json.Unmarshal(b, &cv)
			if len(cv.Playable) == 0 {
				continue
			}
			payload["card"] = cv.Playable[0].ID
			if cv.Playable[0].Kind == "recolor" {
				if f, ok := findField(s, "color"); ok && len(f.Options) > 0 {
					payload["color"] = f.Options[0].Value
				}
			}
		case "search":
			if f, ok := findField(s, "kind"); ok && !f.Required {
				// An optional category can be omitted for a random draw.
				optionalKind = true
			}
			if !optionalKind {
				if c, exists := clues(v); exists {
					for _, l := range c.Locations {
						if l.Remaining > 0 && (len(s.Options) == 0 || slices.ContainsFunc(s.Options, func(o engine.Option) bool { return o.Value == l.ID })) {
							value = l.ID
							break
						}
					}
					if value == "" && len(s.Options) > 0 {
						value = s.Options[0].Value
					}
					if value == "" && c.Mode != "location" && len(c.Pool) > 0 {
						value = c.Pool[0].ID
					}
				} else if len(s.Options) > 0 {
					value = s.Options[0].Value
				}
			}
		case "vote":
			if len(s.Options) > 0 {
				value = s.Options[0].Value
			}
		case "dice.ask":
			if len(s.Options) > 0 {
				value = s.Options[0].Value
			}
		}
		if typ == "vote.cast" {
			question, option := firstVote(s, v)
			payload["question"], payload["option"] = question, option
		} else if typ != "card.play" && !optionalKind {
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

func findField(s engine.ActionSpec, key string) (engine.Field, bool) {
	for _, f := range s.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return engine.Field{}, false
}
