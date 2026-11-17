package reveal

import (
	"strings"

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
		results := vote.Results(c)
		v := View{Truth: c.Script().Truth, Title: c.Step().Title, Results: results}
		outcomes := map[string]vote.Result{}
		for _, result := range results {
			outcomes[result.Question] = result
		}
		// Prefer specific matching endings over the empty fallback.
		best := -1
		for _, ending := range c.Script().Endings {
			match, specificity := true, 0
			for question, condition := range ending.When {
				result, exists := outcomes[question]
				switch {
				case condition == "*":
				case condition == "correct", condition == "wrong":
					specificity++
					if !exists || result.Correct != (condition == "correct") {
						match = false
					}
				case strings.HasPrefix(condition, "winner:"):
					specificity++
					if result.Winner != strings.TrimPrefix(condition, "winner:") {
						match = false
					}
				case strings.HasPrefix(condition, "option:"):
					specificity++
					if result.Winner != strings.TrimPrefix(condition, "option:") {
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
		if v.Text == v.Truth {
			v.Text = ""
		}
		c.Set("reveal", v)
		c.Emit("reveal", "", engine.Public(), map[string]any{"truth": v.Truth, "title": v.Title, "text": v.Text, "results": results})
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
