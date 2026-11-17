package privatechat

import (
	"fmt"

	"jbs/internal/engine"
	"jbs/internal/plugins/input"
)

type Plugin struct{}

func (Plugin) Name() string { return "privatechat" }
func (Plugin) Init(r *engine.Registry) error {
	r.Action("pm.send", func(c *engine.Ctx, a engine.Action) error {
		if !active(c) || a.Actor == engine.HostID {
			return engine.ErrNotAllowed
		}
		var p struct {
			To   string `json:"to"`
			Text string `json:"text"`
		}
		if err := a.Decode(&p); err != nil {
			return err
		}
		target := c.Seat(p.To)
		if target == nil || !target.Joined || p.To == a.Actor {
			return fmt.Errorf("private message target must be another joined player")
		}
		if err := input.Text(p.Text); err != nil {
			return err
		}
		c.Emit("pm.send", a.Actor, engine.To(a.Actor, p.To), map[string]any{"to": p.To, "text": p.Text})
		return nil
	})
	r.Allowed(func(c *engine.Ctx, pid string) []engine.ActionSpec {
		if !active(c) || pid == engine.HostID || len(targets(c, pid)) == 0 {
			return nil
		}
		return []engine.ActionSpec{{Type: "pm.send", Desc: "Send a private message", Params: map[string]string{"to": "Joined player ID", "text": "Up to 4000 runes"}, Options: targets(c, pid), Optional: true}}
	})
	r.Hint(func(c *engine.Ctx, pid string) string {
		if active(c) && pid != engine.HostID && len(targets(c, pid)) > 0 {
			return "Optionally send a private message to another joined player."
		}
		return ""
	})
	r.View(func(c *engine.Ctx, pid string) (string, any) {
		if active(c) && pid != engine.HostID {
			return "privatechat", map[string]any{"targets": targets(c, pid)}
		}
		return "", nil
	})
	r.Panel(engine.Panel{ID: "privatechat", Plugin: "privatechat", Kind: "privatechat", Title: "Private messages", Phases: []string{"discussion", "search"}, DataKey: "privatechat", Order: 40})
	return nil
}

func active(c *engine.Ctx) bool { return c.PhaseType() == "discussion" || c.PhaseType() == "search" }
func targets(c *engine.Ctx, pid string) []engine.Option {
	out := []engine.Option{}
	for _, s := range c.Seats() {
		if s.Joined && s.ID != pid {
			out = append(out, engine.Option{Value: s.ID, Label: s.CharName})
		}
	}
	return out
}
