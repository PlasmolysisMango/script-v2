package media

import "jbs/internal/engine"

type Plugin struct{}

type View struct {
	AudioURL   string `json:"audio_url,omitempty"`
	AudioLabel string `json:"audio_label,omitempty"`
	Title      string `json:"title,omitempty"`
}

func (Plugin) Name() string { return "media" }

func (Plugin) Init(r *engine.Registry) error {
	r.View(func(c *engine.Ctx, _ string) (string, any) {
		step := c.Step()
		url := step.Str("audio_url", step.Str("audioUrl", ""))
		if url == "" {
			return "", nil
		}
		return "media", View{
			AudioURL:   url,
			AudioLabel: step.Str("audio_label", step.Str("audioLabel", "配套音频")),
			Title:      step.Title,
		}
	})
	return nil
}
