package timer

import (
	"time"

	"jbs/internal/engine"
)

type Plugin struct{}

func (Plugin) Name() string { return "timer" }
func (Plugin) Init(r *engine.Registry) error {
	r.Hook(engine.Hooks{OnPhaseEnter: func(c *engine.Ctx) {
		// Timeout is a number of seconds (including fractional seconds).
		var seconds float64
		switch v := c.Step().Params["timeout"].(type) {
		case int:
			seconds = float64(v)
		case float64:
			seconds = v
		}
		// Bound the conversion to a positive, representable duration.
		if !(seconds > 0 && seconds < float64(1<<63-1)/float64(time.Second)) {
			return
		}
		d := time.Duration(seconds * float64(time.Second))
		if d <= 0 {
			return
		}
		c.SetDeadline(d)
		c.Schedule(d, func(c *engine.Ctx) {
			c.Emit("phase.timeout", "", engine.Public(), map[string]any{"id": c.PhaseID()})
			c.Advance()
		})
	}})
	return nil
}
