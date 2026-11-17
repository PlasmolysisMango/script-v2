package chat

import (
	"jbs/internal/engine"
	"jbs/internal/plugins/input"
	"jbs/internal/plugins/ready"
)

type Plugin struct{}

type Turn struct {
	PID         string `json:"pid"`
	Round       int    `json:"round"`
	TotalRounds int    `json:"total_rounds"`
}

type state struct {
	Turn
	Index int
	Free  bool
	Done  bool
	Order []string
}

func (Plugin) Name() string { return "chat" }
func (Plugin) Init(r *engine.Registry) error {
	pt := engine.PhaseType{OnEnter: enter, EndIf: func(c *engine.Ctx) bool {
		s := get(c)
		return s != nil && ((!s.Free && s.Done) || (s.Free && ready.AllReady(c)))
	}, Allowed: allowed, Hint: func(c *engine.Ctx, pid string) string {
		s := get(c)
		if s != nil && s.Free {
			return "Speak freely, then mark ready when finished."
		}
		if s != nil && s.PID == pid {
			return "Your turn: say something (up to 4000 runes), or pass."
		}
		return "Wait for your turn."
	}}
	r.PhaseType("intro", pt)
	r.PhaseType("discussion", pt)
	r.PhaseType("whisper", engine.PhaseType{
		OnEnter: func(c *engine.Ctx) {
			ready.Reset(c)
			c.Emit("narration", "", engine.Public(), map[string]any{
				"text": c.Step().Str("narration", "Private discussion: send a private message, then mark ready."),
			})
		},
		EndIf:   ready.AllReady,
		Allowed: ready.Allowed,
		Hint: func(c *engine.Ctx, pid string) string {
			if len(ready.Allowed(c, pid)) > 0 {
				return "Use private messages to talk with the others, then mark ready."
			}
			return "Waiting for the other players to finish private discussion."
		},
	})
	r.Action("say", say)
	r.Action("pass", func(c *engine.Ctx, a engine.Action) error {
		s := get(c)
		if s == nil || s.Free || s.Done || s.PID != a.Actor {
			return engine.ErrNotAllowed
		}
		c.Emit("chat.pass", a.Actor, engine.Public(), map[string]any{"round": s.Round})
		next(c, s)
		return nil
	})
	r.View(func(c *engine.Ctx, _ string) (string, any) {
		if s := get(c); s != nil {
			return "turn", s.Turn
		}
		return "", nil
	})
	r.Panel(engine.Panel{ID: "chat", Plugin: "chat", Kind: "chat", Title: "Conversation", Phases: []string{"intro", "discussion"}, DataKey: "turn", Order: 30})
	return nil
}

func get(c *engine.Ctx) *state {
	if c.PhaseType() != "intro" && c.PhaseType() != "discussion" {
		return nil
	}
	s, _ := c.Get("chat.state").(*state)
	return s
}

func enter(c *engine.Ctx) {
	ready.Reset(c)
	s := &state{Turn: Turn{Round: 1, TotalRounds: max(1, c.Step().Int("rounds", 1))}, Free: c.Step().Bool("free", false), Order: order(c)}
	c.Set("chat.state", s)
	if s.Free {
		return
	}
	if len(s.Order) == 0 {
		s.Done = true
		return
	}
	s.PID = s.Order[0]
	start(c, s)
}

func order(c *engine.Ctx) []string {
	var explicit []any
	if v, ok := c.Step().Params["order_role_ids"].([]any); ok {
		explicit = v
	} else if v, ok := c.Step().Params["orderRoleIds"].([]any); ok {
		explicit = v
	}
	var out []string
	seen := map[string]bool{}
	add := func(pid string) {
		if !seen[pid] {
			if seat := c.Seat(pid); seat != nil && seat.Joined {
				seen[pid] = true
				out = append(out, pid)
			}
		}
	}
	for _, v := range explicit {
		if pid, ok := v.(string); ok {
			add(pid)
		}
	}
	if len(out) == 0 {
		for _, seat := range c.Seats() {
			if !seen[seat.ID] {
				seen[seat.ID] = true
				out = append(out, seat.ID)
			}
		}
	}
	if c.Step().Str("order", "") == "random" {
		c.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	}
	return out
}

func allowed(c *engine.Ctx, pid string) []engine.ActionSpec {
	s := get(c)
	if s == nil || s.Done {
		return nil
	}
	if s.Free {
		out := []engine.ActionSpec{{Type: "say", Desc: "Speak", Params: map[string]string{"text": "Up to 4000 runes"}, Optional: true}}
		return append(out, ready.Allowed(c, pid)...)
	}
	if s.PID != pid {
		return nil
	}
	return []engine.ActionSpec{{Type: "say", Desc: "Speak", Params: map[string]string{"text": "Up to 4000 runes"}}, {Type: "pass", Desc: "Pass your turn"}}
}

func say(c *engine.Ctx, a engine.Action) error {
	s := get(c)
	if s == nil || s.Done || (!s.Free && s.PID != a.Actor) {
		return engine.ErrNotAllowed
	}
	var p struct {
		Text string `json:"text"`
	}
	if err := a.Decode(&p); err != nil {
		return err
	}
	if err := input.Text(p.Text); err != nil {
		return err
	}
	c.Emit("chat.say", a.Actor, engine.Public(), map[string]any{"text": p.Text, "round": s.Round})
	if !s.Free {
		next(c, s)
	}
	return nil
}

func next(c *engine.Ctx, s *state) {
	s.Index++
	if s.Index >= len(s.Order) {
		s.Index = 0
		s.Round++
	}
	if s.Round > s.TotalRounds || len(s.Order) == 0 {
		s.Done = true
		s.PID = ""
		return
	}
	s.PID = s.Order[s.Index]
	start(c, s)
}

func start(c *engine.Ctx, s *state) {
	c.Emit("turn.start", s.PID, engine.Public(), map[string]any{"pid": s.PID, "round": s.Round, "total_rounds": s.TotalRounds})
}
