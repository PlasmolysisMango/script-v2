package protocol

import "jbs/internal/engine"

type Message struct {
	Type   string         `json:"t"`
	ID     string         `json:"id,omitempty"`
	Action *engine.Action `json:"action,omitempty"`
	Event  *engine.Event  `json:"event,omitempty"`
	View   *engine.View   `json:"view,omitempty"`
	OK     bool           `json:"ok,omitempty"`
	Error  string         `json:"error,omitempty"`
}
