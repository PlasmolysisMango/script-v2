package plugins

import (
	"jbs/internal/engine"
	"jbs/internal/plugins/cards"
	"jbs/internal/plugins/chat"
	"jbs/internal/plugins/clues"
	"jbs/internal/plugins/dice"
	"jbs/internal/plugins/host"
	"jbs/internal/plugins/media"
	"jbs/internal/plugins/opening"
	"jbs/internal/plugins/points"
	"jbs/internal/plugins/privatechat"
	"jbs/internal/plugins/reading"
	"jbs/internal/plugins/ready"
	"jbs/internal/plugins/reveal"
	"jbs/internal/plugins/timer"
	"jbs/internal/plugins/tts"
	"jbs/internal/plugins/vote"
)

func NewRegistry() (*engine.Registry, error) {
	return engine.NewRegistry(ready.Plugin{}, reading.Plugin{}, timer.Plugin{}, chat.Plugin{},
		clues.Plugin{}, points.Plugin{}, vote.Plugin{}, reveal.Plugin{}, tts.Plugin{}, privatechat.Plugin{},
		opening.Plugin{}, host.Plugin{}, media.Plugin{}, dice.Plugin{}, cards.Plugin{})
}
