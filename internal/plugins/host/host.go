package host

import (
	"fmt"

	"jbs/internal/engine"
	"jbs/internal/plugins/ready"
)

type Plugin struct{}

type View struct {
	DMScript string           `json:"dm_script,omitempty"`
	Facts    []map[string]any `json:"facts,omitempty"`
	Title    string           `json:"title"`
	Hint     string           `json:"hint,omitempty"`
	Votes    []string         `json:"vote_ids,omitempty"`
}

func (Plugin) Name() string { return "host" }
func (Plugin) Init(r *engine.Registry) error {
	r.PhaseType("host", engine.PhaseType{
		OnEnter: func(c *engine.Ctx) {
			ready.Reset(c)
			c.Emit("narration", "", engine.Public(), map[string]any{"text": c.Step().Str("narration", "主持人阶段："+c.Step().Title)})
		},
		Allowed: ready.Allowed,
		Hint: func(c *engine.Ctx, pid string) string {
			if pid == engine.HostID {
				return "Read the DM notes in the host panel, then advance."
			}
			return "Waiting for the host to resolve this scene."
		},
	})
	r.Action("host.narrate", func(c *engine.Ctx, a engine.Action) error {
		if a.Actor != engine.HostID {
			return engine.ErrNotAllowed
		}
		var p struct {
			Text string `json:"text"`
		}
		if err := a.Decode(&p); err != nil {
			return err
		}
		if p.Text == "" {
			return fmt.Errorf("narration must not be empty")
		}
		c.Emit("narration", "", engine.Public(), map[string]any{"text": p.Text})
		return nil
	})
	r.Allowed(func(c *engine.Ctx, pid string) []engine.ActionSpec {
		if pid != engine.HostID {
			return nil
		}
		return []engine.ActionSpec{{Type: "host.narrate", Desc: "Narrate to everyone", Params: map[string]string{"text": "Narration"}, Optional: true}}
	})
	r.View(func(c *engine.Ctx, pid string) (string, any) {
		if pid != engine.HostID {
			return "", nil
		}
		v := View{Title: c.Step().Title, DMScript: c.Step().Str("dm_script", ""), Hint: c.Step().Str("host_hint", "")}
		for _, fact := range c.Script().HostFacts {
			v.Facts = append(v.Facts, map[string]any{"id": fact.ID, "text": fact.Text})
		}
		if raw, ok := c.Step().Params["vote_ids"].([]any); ok {
			for _, id := range raw {
				if s, ok := id.(string); ok {
					v.Votes = append(v.Votes, s)
				}
			}
		}
		return "host", v
	})
	r.Panel(engine.Panel{ID: "host", Plugin: "host", Kind: "host", Title: "Host notes", DataKey: "host", HostOnly: true, Order: 5})
	return nil
}
