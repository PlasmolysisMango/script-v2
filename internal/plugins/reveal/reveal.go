package reveal

import (
	"jbs/internal/engine"
	"jbs/internal/plugins/vote"
)

type Plugin struct{}

type View struct {
	Truth   string        `json:"truth"`
	Title   string        `json:"title"`
	Text    string        `json:"text"`
	Results []vote.Result `json:"results"`
}

func (Plugin) Name() string { return "reveal" }
func (Plugin) Init(r *engine.Registry) error {
	r.PhaseType("reveal", engine.PhaseType{OnEnter: func(c *engine.Ctx) {
		v := View{Truth: c.Script().Truth, Title: c.Step().Title, Results: vote.Results(c)}
		outcomes := map[string]bool{}
		for _, result := range v.Results {
			outcomes[result.Question] = result.Correct
		}
		// Prefer specific matching endings over a wildcard fallback.
		best := -1
		for _, ending := range c.Script().Endings {
			match, specificity := true, 0
			for question, condition := range ending.When {
				correct, exists := outcomes[question]
				switch condition {
				case "*":
				case "correct", "wrong":
					specificity++
					if !exists || correct != (condition == "correct") {
						match = false
					}
				default:
					match = false
				}
			}
			if match && specificity > best {
				best, v.Title, v.Text = specificity, ending.Title, ending.Text
			}
		}
		c.Set("reveal", v)
		c.Emit("reveal", "", engine.Public(), map[string]any{"truth": v.Truth, "title": v.Title, "text": v.Text, "results": vote.Results(c)})
		for _, step := range c.Script().Flow {
			if step.Type == "end" {
				c.Goto(step.ID)
				return
			}
		}
		c.EndGame()
	}})
	r.View(func(c *engine.Ctx, _ string) (string, any) {
		v, ok := c.Get("reveal").(View)
		if !ok {
			return "", nil
		}
		v.Results = vote.Results(c)
		return "reveal", v
	})
	r.Panel(engine.Panel{ID: "reveal", Plugin: "reveal", Kind: "reveal", Title: "The truth", Phases: []string{"reveal", "end"}, DataKey: "reveal", Order: 60})
	return nil
}
