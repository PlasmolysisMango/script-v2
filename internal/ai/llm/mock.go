package llm

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
)

// Mock produces deterministic structured actions from the same filtered JSON
// request supplied to Qoder. It never accepts a Script or an answer key.
type Mock struct{}

type mockOption struct {
	Value string `json:"value"`
}

type mockField struct {
	Key      string       `json:"key"`
	Options  []mockOption `json:"options"`
	Required bool         `json:"required"`
}

type mockSpec struct {
	Type     string            `json:"type"`
	Params   map[string]string `json:"params"`
	Optional bool              `json:"optional"`
	Options  []mockOption      `json:"options"`
	Fields   []mockField       `json:"fields"`
}

type mockRequest struct {
	Mode          string     `json:"mode"`
	ActionOptions []mockSpec `json:"action_options"`
	Data          struct {
		Votes struct {
			Questions []struct {
				ID      string       `json:"id"`
				Options []mockOption `json:"options"`
			} `json:"questions"`
			Cast map[string]string `json:"cast"`
		} `json:"votes"`
		Clues struct {
			AP        int `json:"ap"`
			Locations []struct {
				ID        string `json:"id"`
				Remaining int    `json:"remaining"`
			} `json:"locations"`
		} `json:"clues"`
		Dice struct {
			Question string            `json:"question"`
			Keywords map[string]string `json:"keywords"`
		} `json:"dice"`
		Cards struct {
			Playable []struct {
				ID    string `json:"id"`
				Kind  string `json:"kind"`
				Color string `json:"color"`
			} `json:"playable"`
		} `json:"cards"`
	} `json:"data"`
}

func (Mock) Complete(ctx context.Context, system, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var request mockRequest
	if json.Unmarshal([]byte(prompt), &request) != nil {
		return "", ErrResponse
	}
	if request.Mode == "summary" {
		return "已听取各人的公开说法，仍需对照现有线索核实，尚不能确定真相。", nil
	}
	for _, typ := range []string{"search", "dice.ask", "dice.answer", "card.play", "card.draw", "card.pass", "vote.cast", "vote", "say", "pass", "ready"} {
		for _, s := range request.ActionOptions {
			if s.Type != typ || s.Optional {
				continue
			}
			payload, ok := request.payload(s)
			if !ok {
				continue
			}
			b, _ := json.Marshal(struct {
				Action  string            `json:"action"`
				Payload map[string]string `json:"payload"`
				Thought string            `json:"thought"`
			}{typ, payload, "先核实现有线索，不把怀疑当成事实。"})
			return string(b), nil
		}
	}
	return "", ErrResponse
}

func (r mockRequest) payload(s mockSpec) (map[string]string, bool) {
	payload := map[string]string{}
	first := func(key string) string {
		for _, f := range s.Fields {
			if f.Key == key && len(f.Options) > 0 {
				return f.Options[0].Value
			}
		}
		if len(s.Options) > 0 {
			return s.Options[0].Value
		}
		return ""
	}
	required := func(key string) bool {
		for _, f := range s.Fields {
			if f.Key == key {
				return f.Required
			}
		}
		_, ok := s.Params[key]
		return ok
	}
	switch s.Type {
	case "say":
		payload["text"] = "我想听听各位掌握的线索，再核对彼此的说法。"
	case "search":
		switch {
		case s.Params["clue"] != "":
			payload["clue"] = first("clue")
		case s.Params["location"] != "":
			chosen := ""
			for _, l := range r.Data.Clues.Locations {
				if l.Remaining <= 0 {
					continue
				}
				for _, o := range s.Options {
					if o.Value == l.ID {
						chosen = l.ID
						break
					}
				}
				if chosen != "" {
					break
				}
			}
			if chosen == "" {
				chosen = first("location")
			}
			payload["location"] = chosen
		case s.Params["kind"] != "":
			payload["kind"] = first("kind")
		case len(s.Fields) > 0:
			// Category fields are optional; an empty payload draws randomly.
		case len(s.Options) > 0:
			payload["location"] = first("location")
		}
		if len(payload) == 0 {
			if len(s.Fields) > 0 || (len(s.Options) == 0 && len(s.Params) == 0) {
				return payload, true
			}
			return nil, false
		}
	case "dice.ask":
		q := first("question")
		if q == "" {
			return nil, false
		}
		payload["question"] = q
	case "dice.answer":
		text := ""
		keys := make([]string, 0, len(r.Data.Dice.Keywords))
		for trigger := range r.Data.Dice.Keywords {
			keys = append(keys, trigger)
		}
		slices.Sort(keys)
		for _, trigger := range keys {
			if strings.Contains(r.Data.Dice.Question, trigger) {
				text = r.Data.Dice.Keywords[trigger]
				break
			}
		}
		if text == "" {
			text = "我确认上述内容属实，并愿意继续说明。"
		}
		payload["text"] = text
	case "card.play":
		if len(r.Data.Cards.Playable) == 0 {
			return nil, false
		}
		card := r.Data.Cards.Playable[0]
		payload["card"] = card.ID
		if card.Kind == "recolor" {
			payload["color"] = first("color")
		}
	case "card.draw", "card.pass", "ready", "pass":
		return payload, true
	case "vote.cast":
		question, option := "", ""
		for _, allowed := range s.Options {
			if _, cast := r.Data.Votes.Cast[allowed.Value]; cast {
				continue
			}
			for _, q := range r.Data.Votes.Questions {
				if q.ID == allowed.Value && len(q.Options) > 0 {
					question, option = q.ID, q.Options[0].Value
					break
				}
			}
			if question != "" {
				break
			}
		}
		if question == "" {
			return nil, false
		}
		payload["question"], payload["option"] = question, option
	case "vote":
		option := first("option")
		if option == "" {
			return nil, false
		}
		payload["option"] = option
	default:
		keys := make([]string, 0, len(s.Fields)+len(s.Params))
		for _, f := range s.Fields {
			keys = append(keys, f.Key)
		}
		for k := range s.Params {
			if len(s.Fields) == 0 {
				keys = append(keys, k)
			}
		}
		slices.Sort(keys)
		for _, key := range keys {
			if !required(key) {
				continue
			}
			v := first(key)
			if v == "" {
				return nil, false
			}
			payload[key] = v
		}
	}
	if s.Type != "search" {
		for _, f := range s.Fields {
			if f.Required {
				if payload[f.Key] == "" {
					return nil, false
				}
			}
		}
		for key := range s.Params {
			if payload[key] == "" {
				return nil, false
			}
		}
	}
	return payload, true
}
