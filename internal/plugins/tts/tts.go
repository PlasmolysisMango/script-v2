package tts

import (
	"slices"
	"strings"

	"jbs/internal/engine"
	"jbs/internal/script"
)

type Plugin struct{}

func (Plugin) Name() string { return "tts" }
func (Plugin) Init(r *engine.Registry) error {
	r.Hook(engine.Hooks{OnEvent: func(c *engine.Ctx, e engine.Event) {
		var text string
		switch e.Type {
		case "narration", "chat.say":
			text = e.Str("text")
		case "reveal":
			text = strings.TrimSpace(e.Str("truth") + "\n\n" + e.Str("text"))
		default:
			return // In particular, never process our own tts events.
		}
		voice := script.Voice{Hint: "narrator", Rate: 1, Pitch: 1}
		if ch := c.Script().Character(e.Actor); ch != nil {
			voice = ch.TTS
		}
		c.Emit("tts", e.Actor, engine.Audience{Public: e.Public, To: slices.Clone(e.To)}, map[string]any{
			"text": text, "voice_hint": voice.Hint, "rate": voice.Rate, "pitch": voice.Pitch,
			"source": e.Type, "source_seq": e.Seq,
		})
	}})
	return nil
}
