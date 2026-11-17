package opening

import (
	"jbs/internal/engine"
	"jbs/internal/plugins/clues"
	"jbs/internal/plugins/points"
	"jbs/internal/plugins/ready"
)

type Plugin struct{}

func (Plugin) Name() string { return "opening" }
func (Plugin) Init(r *engine.Registry) error {
	r.PhaseType("opening", engine.PhaseType{
		OnEnter: func(c *engine.Ctx) {
			ready.Reset(c)
			clues.DistributePrivate(c, stepList(c.Step().Params["private_clues"]))
			if c.Step().Bool("settle_favor", false) || c.Step().Bool("settleFavor", false) {
				points.Settle(c)
				if throne := c.Step().Str("throne_role_id", c.Step().Str("throneRoleId", "")); throne != "" {
					if seat := c.Seat(throne); seat != nil {
						points.SetThrone(c, throne)
						c.Emit("points.throne", "", engine.Public(), map[string]any{"target": throne})
					}
				}
			}
			c.Emit("narration", "", engine.Public(), map[string]any{
				"text": c.Step().Str("narration", c.Step().Title+"。等待主持人推进。"),
			})
		},
		Allowed: ready.Allowed,
		Hint: func(c *engine.Ctx, pid string) string {
			if len(ready.Allowed(c, pid)) > 0 {
				return "Read the opening, then mark ready. The host will advance."
			}
			return "Waiting for the host."
		},
	})
	r.Panel(engine.Panel{ID: "opening", Plugin: "opening", Kind: "opening", Title: "Opening", Phases: []string{"opening"}, Order: 15})
	return nil
}

func stepList(v any) []string {
	switch x := v.(type) {
	case []any:
		var out []string
		for _, item := range x {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return x
	}
	return nil
}
