package ready

import "jbs/internal/engine"

type Plugin struct{}

func (Plugin) Name() string { return "ready" }
func (Plugin) Init(r *engine.Registry) error {
	r.Action("ready", func(c *engine.Ctx, a engine.Action) error {
		if !supports(c) || c.Seat(a.Actor) == nil || !c.Seat(a.Actor).Joined || c.Seat(a.Actor).Ready {
			return engine.ErrNotAllowed
		}
		Mark(c, a.Actor)
		return nil
	})
	r.Action("host.advance", func(c *engine.Ctx, a engine.Action) error {
		if a.Actor != engine.HostID {
			return engine.ErrNotAllowed
		}
		c.Advance()
		return nil
	})
	r.Allowed(func(c *engine.Ctx, pid string) []engine.ActionSpec {
		if pid == engine.HostID {
			return []engine.ActionSpec{{Type: "host.advance", Desc: "Advance to the next phase"}}
		}
		return nil
	})
	r.PhaseType("ready_check", engine.PhaseType{OnEnter: Reset, EndIf: AllReady, Allowed: Allowed, Hint: Hint})
	r.Panel(engine.Panel{ID: "ready", Plugin: "ready", Kind: "ready", Title: "Ready", Phases: []string{"ready_check", "reading", "search", "opening", "whisper"}, Order: 90})
	return nil
}

func supports(c *engine.Ctx) bool {
	switch c.PhaseType() {
	case "ready_check", "reading", "search", "opening", "whisper", "whisper_check":
		return true
	case "intro", "discussion":
		return c.Step().Bool("free", false)
	}
	return false
}

// Reset, AllReady, Allowed and Mark let phases share a single ready action.
func Reset(c *engine.Ctx) {
	for _, s := range c.Seats() {
		s.Ready = false
	}
}

func AllReady(c *engine.Ctx) bool {
	if !c.AllJoined() {
		return false
	}
	for _, s := range c.Seats() {
		if !s.Ready {
			return false
		}
	}
	return true
}

func Allowed(c *engine.Ctx, pid string) []engine.ActionSpec {
	if s := c.Seat(pid); s != nil && s.Joined && !s.Ready {
		return []engine.ActionSpec{{Type: "ready", Desc: "Finish this phase"}}
	}
	return nil
}

func Mark(c *engine.Ctx, pid string) {
	if s := c.Seat(pid); s != nil && s.Joined && !s.Ready {
		s.Ready = true
		c.Emit("player.ready", pid, engine.Public(), nil)
	}
}

func Hint(c *engine.Ctx, pid string) string {
	if len(Allowed(c, pid)) > 0 {
		return "Mark ready when finished; the phase ends when everyone is ready."
	}
	return "Waiting for the other players."
}
