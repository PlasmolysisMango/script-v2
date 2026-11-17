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

func (Mock) Complete(ctx context.Context, system, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var request struct {
		Mode          string `json:"mode"`
		ActionOptions []struct {
			Type     string            `json:"type"`
			Params   map[string]string `json:"params"`
			Optional bool              `json:"optional"`
			Options  []struct {
				Value string `json:"value"`
			} `json:"options"`
		} `json:"action_options"`
		Data struct {
			Votes struct {
				Questions []struct {
					ID      string `json:"id"`
					Options []struct {
						Value string `json:"value"`
					} `json:"options"`
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
		} `json:"data"`
	}
	if json.Unmarshal([]byte(prompt), &request) != nil {
		return "", ErrResponse
	}
	if request.Mode == "summary" {
		return "已听取各人的公开说法，仍需对照现有线索核实，尚不能确定真相。", nil
	}
	for _, typ := range []string{"ready", "say", "pass", "search", "vote.cast", "vote"} {
		for _, s := range request.ActionOptions {
			if s.Type != typ || s.Optional {
				continue
			}
			keys := make([]string, 0, len(s.Params))
			for k := range s.Params {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			value, question := "", ""
			switch typ {
			case "say":
				value = "我想听听各位掌握的线索，再核对彼此的说法。"
				if len(keys) == 0 {
					keys = []string{"text"}
				}
			case "search":
				if request.Data.Clues.AP > 0 {
					for _, l := range request.Data.Clues.Locations {
						if l.Remaining <= 0 {
							continue
						}
						available := len(s.Options) == 0
						for _, o := range s.Options {
							available = available || o.Value == l.ID
						}
						if available {
							value = l.ID
							break
						}
					}
				} else if len(s.Options) > 0 && len(request.Data.Clues.Locations) == 0 {
					value = s.Options[0].Value
				}
				if len(keys) == 0 {
					keys = []string{"location"}
				}
			case "vote.cast":
				for _, allowed := range s.Options {
					if _, cast := request.Data.Votes.Cast[allowed.Value]; cast {
						continue
					}
					for _, q := range request.Data.Votes.Questions {
						if q.ID == allowed.Value && len(q.Options) > 0 {
							question, value = q.ID, q.Options[0].Value
							break
						}
					}
					if question != "" {
						break
					}
				}
				if len(keys) == 0 {
					keys = []string{"option", "question"}
				}
			case "vote":
				if len(s.Options) > 0 {
					value = s.Options[0].Value
				}
				if len(keys) == 0 {
					keys = []string{"option"}
				}
			}
			if len(keys) > 0 && strings.TrimSpace(value) == "" {
				continue
			}
			payload := map[string]string{}
			for _, key := range keys {
				payload[key] = value
				if typ == "vote.cast" && key == "question" {
					payload[key] = question
				}
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
