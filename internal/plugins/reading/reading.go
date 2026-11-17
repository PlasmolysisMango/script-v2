package reading

import (
	"jbs/internal/engine"
	"jbs/internal/plugins/ready"
)

type Plugin struct{}

func (Plugin) Name() string { return "reading" }
func (Plugin) Init(r *engine.Registry) error {
	r.PhaseType("reading", engine.PhaseType{
		OnEnter: func(c *engine.Ctx) {
			ready.Reset(c)
			act := c.Act()
			previous, _ := c.Get("reading.act").(int)
			c.Set("reading.act", max(previous, act))
			c.Emit("narration", "", engine.Public(), map[string]any{
				"act": act, "title": c.Step().Title,
				"text": c.Step().Str("narration", c.Step().Str("text", c.Step().Title)),
			})
			for _, ch := range c.Script().Characters {
				for _, sec := range ch.Sections {
					if sec.Act == act {
						c.Emit("script.section", ch.ID, engine.To(ch.ID), map[string]any{
							"act": sec.Act, "title": sec.Title, "body": sec.Body,
						})
					}
				}
			}
		},
		EndIf: ready.AllReady, Allowed: ready.Allowed, Hint: ready.Hint,
	})
	r.Panel(engine.Panel{ID: "script", Plugin: "reading", Kind: "script", Title: "Your script", Order: 10})
	return nil
}
