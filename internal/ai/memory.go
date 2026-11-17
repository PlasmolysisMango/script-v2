package ai

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"strings"
	"text/template"
	"unicode/utf8"

	"jbs/internal/ai/llm"
	"jbs/internal/engine"
)

//go:embed prompts/*.tmpl
var promptFiles embed.FS

var templates = template.Must(template.ParseFS(promptFiles, "prompts/*.tmpl"))

const summaryThreshold = 6000
const maxHistoryChars = 12000
const maxSummaryChars = 2000
const maxNotesChars = 1500

type message struct {
	Seq   int64  `json:"seq"`
	Type  string `json:"type"`
	Actor string `json:"actor,omitempty"`
	Text  string `json:"text"`
}

type memory struct {
	since   int64
	history []message
	summary string
	notes   string
}

func (m *memory) ingest(events []engine.Event, through int64) {
	for _, e := range events {
		if e.Seq <= m.since || e.Seq > through || (!e.Public && e.Type != "pm.send") {
			continue
		}
		switch e.Type {
		case "chat.say", "narration", "pm.send":
			text := e.Str("text")
			if text != "" {
				m.history = append(m.history, message{Seq: e.Seq, Type: e.Type, Actor: e.Actor, Text: clip(text, 3000)})
			}
		case "chat.pass":
			m.history = append(m.history, message{Seq: e.Seq, Type: e.Type, Actor: e.Actor, Text: "暂不发言"})
		}
	}
	m.since = max(m.since, through)
	for len(m.history) > 32 || (historyChars(m.history) > maxHistoryChars && len(m.history) > 1) {
		m.history = m.history[1:]
	}
}

func (m *memory) summarize(ctx context.Context, p llm.Provider) error {
	if historyChars(m.history) < summaryThreshold {
		return nil
	}
	// Summarize the older prefix while retaining recent messages verbatim.
	cut := len(m.history)
	recentChars := 0
	for cut > 1 && len(m.history)-cut < 6 {
		n := utf8.RuneCountInString(m.history[cut-1].Text)
		if recentChars+n > 3000 {
			break
		}
		cut--
		recentChars += n
	}
	b, _ := json.Marshal(struct {
		Mode    string    `json:"mode"`
		Summary string    `json:"summary"`
		History []message `json:"history"`
	}{"summary", m.summary, m.history[:cut]})
	system, _ := render("summary.tmpl", nil)
	text, err := complete(ctx, p, system, string(b))
	if err != nil || strings.TrimSpace(text) == "" || len(text) > llm.MaxOutputBytes {
		// A failing summarizer must not cause unbounded prompts or retry storms.
		m.history = m.history[cut:]
		if err == nil {
			err = llm.ErrResponse
		}
		return err
	}
	m.summary = clip(strings.TrimSpace(text), maxSummaryChars)
	m.history = m.history[cut:]
	return nil
}

func (m *memory) prompt(v engine.View, correction string) (string, error) {
	data := v.Data
	actions := turnActions(v)
	request := struct {
		Mode          string                `json:"mode"`
		PID           string                `json:"pid"`
		Meta          any                   `json:"meta"`
		Phase         engine.PhaseView      `json:"phase"`
		StateID       int64                 `json:"state_id"`
		Hint          string                `json:"plugin_instructions,omitempty"`
		Me            *engine.CharacterView `json:"me"`
		Seats         []engine.Seat         `json:"seats"`
		Data          map[string]any        `json:"data"`
		ActionOptions []engine.ActionSpec   `json:"action_options"`
		ActionSchema  map[string]any        `json:"action_schema"`
		History       []message             `json:"history"`
		Summary       string                `json:"summary,omitempty"`
		Notes         string                `json:"notes,omitempty"`
		Correction    string                `json:"correction,omitempty"`
	}{Mode: "turn", PID: v.PID, Meta: v.Meta, Phase: v.Phase, StateID: v.StateID,
		Me: v.Me, Hint: v.Hint, Seats: v.Seats, Data: data, ActionOptions: actions, ActionSchema: actionSchema(actions),
		History: m.history, Summary: m.summary, Notes: m.notes}
	request.Correction = correction
	b, err := json.Marshal(request)
	if err != nil {
		return "", ErrInvalidAction
	}
	return render("turn.tmpl", struct{ JSON string }{string(b)})
}

func render(name string, data any) (string, error) {
	var b bytes.Buffer
	err := templates.ExecuteTemplate(&b, name, data)
	return b.String(), err
}

func historyChars(messages []message) int {
	n := 0
	for _, m := range messages {
		n += utf8.RuneCountInString(m.Text)
	}
	return n
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func complete(ctx context.Context, p llm.Provider, system, prompt string) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, llm.DefaultTimeout)
	defer cancel()
	return p.Complete(callCtx, system, prompt)
}
