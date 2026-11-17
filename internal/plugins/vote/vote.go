package vote

import (
	"fmt"
	"slices"

	"jbs/internal/engine"
)

type Plugin struct{}

type Question struct {
	ID       string          `json:"id"`
	Question string          `json:"question"`
	Options  []engine.Option `json:"options"`
}

type Result struct {
	Question string         `json:"question"`
	Counts   map[string]int `json:"counts"`
	Winner   string         `json:"winner"`
	Correct  bool           `json:"correct"`
}

type state struct {
	Questions []Question
	Cast      map[string]map[string]string
	Results   []Result
	Complete  bool
}

func (Plugin) Name() string { return "vote" }
func (Plugin) Init(r *engine.Registry) error {
	r.PhaseType("vote", engine.PhaseType{
		OnEnter: func(c *engine.Ctx) {
			s := &state{Questions: []Question{}, Cast: map[string]map[string]string{}, Results: []Result{}}
			for _, v := range c.Script().Votes {
				q := Question{ID: v.ID, Question: v.Question, Options: []engine.Option{}}
				if v.Options == "characters" || v.Options == "" {
					for _, ch := range c.Script().Characters {
						q.Options = append(q.Options, engine.Option{Value: ch.ID, Label: ch.Name})
					}
				}
				s.Questions = append(s.Questions, q)
			}
			c.Set("vote.state", s)
			c.Set("vote.results", []Result{})
			finish(c, s)
		},
		EndIf: func(c *engine.Ctx) bool { s := get(c); return s != nil && s.Complete },
		Allowed: func(c *engine.Ctx, pid string) []engine.ActionSpec {
			s := get(c)
			if s == nil || s.Complete || len(s.Cast[pid]) == len(s.Questions) {
				return nil
			}
			var questions []engine.Option
			for _, q := range s.Questions {
				if _, cast := s.Cast[pid][q.ID]; !cast {
					questions = append(questions, engine.Option{Value: q.ID, Label: q.Question})
				}
			}
			return []engine.ActionSpec{{Type: "vote.cast", Desc: "Cast a private vote", Params: map[string]string{"question": "Question ID", "option": "Option value"}, Options: questions}}
		},
		Hint: func(_ *engine.Ctx, _ string) string {
			return "Vote on every question. Choices remain private until everyone has voted."
		},
	})
	r.Action("vote.cast", cast)
	r.Hook(engine.Hooks{OnEvent: func(c *engine.Ctx, e engine.Event) {
		if e.Type == "player.join" && c.PhaseType() == "vote" {
			finish(c, get(c))
		}
	}})
	r.View(func(c *engine.Ctx, pid string) (string, any) {
		s := get(c)
		if s == nil {
			return "", nil
		}
		own := map[string]string{}
		for question, option := range s.Cast[pid] {
			own[question] = option
		}
		v := map[string]any{"questions": s.Questions, "cast": own}
		if s.Complete {
			v["results"] = cloneResults(s.Results)
		}
		return "votes", v
	})
	r.Panel(engine.Panel{ID: "votes", Plugin: "vote", Kind: "votes", Title: "Votes", Phases: []string{"vote", "reveal", "end"}, DataKey: "votes", Order: 50})
	return nil
}

func get(c *engine.Ctx) *state { s, _ := c.Get("vote.state").(*state); return s }

func cast(c *engine.Ctx, a engine.Action) error {
	s := get(c)
	if c.PhaseType() != "vote" || s == nil || s.Complete {
		return engine.ErrNotAllowed
	}
	var p struct {
		Question string `json:"question"`
		Option   string `json:"option"`
	}
	if err := a.Decode(&p); err != nil {
		return err
	}
	idx := slices.IndexFunc(s.Questions, func(q Question) bool { return q.ID == p.Question })
	if idx < 0 || !slices.ContainsFunc(s.Questions[idx].Options, func(o engine.Option) bool { return o.Value == p.Option }) {
		return fmt.Errorf("unknown question or option")
	}
	if _, exists := s.Cast[a.Actor][p.Question]; exists {
		return fmt.Errorf("question already answered")
	}
	if s.Cast[a.Actor] == nil {
		s.Cast[a.Actor] = map[string]string{}
	}
	s.Cast[a.Actor][p.Question] = p.Option
	c.Emit("vote.cast", a.Actor, engine.To(a.Actor), map[string]any{"question": p.Question, "option": p.Option})
	finish(c, s)
	return nil
}

func finish(c *engine.Ctx, s *state) {
	if s == nil || s.Complete || !c.AllJoined() {
		return
	}
	for _, seat := range c.Seats() {
		if len(s.Cast[seat.ID]) != len(s.Questions) {
			return
		}
	}
	s.Complete = true
	for _, q := range s.Questions {
		result := Result{Question: q.ID, Counts: map[string]int{}}
		for _, option := range q.Options {
			result.Counts[option.Value] = 0
		}
		for _, seat := range c.Seats() {
			result.Counts[s.Cast[seat.ID][q.ID]]++
		}
		best, tied := 0, false
		for _, option := range q.Options {
			count := result.Counts[option.Value]
			if count > best {
				result.Winner, best, tied = option.Value, count, false
			} else if count == best && count > 0 {
				tied = true
			}
		}
		if tied {
			result.Winner = ""
		}
		for _, v := range c.Script().Votes {
			if v.ID == q.ID {
				result.Correct = v.Correct != "" && result.Winner == v.Correct
				break
			}
		}
		s.Results = append(s.Results, result)
	}
	c.Set("vote.results", cloneResults(s.Results))
	for _, result := range s.Results {
		c.Emit("vote.result", "", engine.Public(), map[string]any{
			"question": result.Question, "counts": cloneCounts(result.Counts), "winner": result.Winner, "correct": result.Correct,
		})
	}
}

// Results returns completed results, without sharing mutable state with callers.
func Results(c *engine.Ctx) []Result {
	results, _ := c.Get("vote.results").([]Result)
	return cloneResults(results)
}

func cloneResults(results []Result) []Result {
	out := make([]Result, len(results))
	for i, result := range results {
		out[i] = result
		out[i].Counts = cloneCounts(result.Counts)
	}
	return out
}

func cloneCounts(counts map[string]int) map[string]int {
	out := map[string]int{}
	for option, n := range counts {
		out[option] = n
	}
	return out
}
