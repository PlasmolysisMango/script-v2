package ai

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"jbs/internal/ai/llm"
	"jbs/internal/engine"
)

func TestParseAction(t *testing.T) {
	valid := `{"action":"say","payload":{"text":"括号 { 与 }，引号 \" 和斜线 \\"},"thought":"私密笔记"}`
	for _, input := range []string{valid, "```json\n" + valid + "\n```", "回答：\n" + valid + "\n结束"} {
		d, err := ParseAction(input)
		if err != nil || d.Action != "say" || d.Thought != "私密笔记" {
			t.Fatalf("parse valid response: %+v, %v", d, err)
		}
	}
	for _, input := range []string{
		`{"action":"ready","payload":{}}`,
		`{"action":"ready","payload":{},"thought":null}`,
		`{"action":"ready","payload":null,"thought":""}`,
		`{"action":"ready","payload":[],"thought":""}`,
		`{"action":"ready","payload":{},"thought":"","extra":1}`,
		`{"action":"ready","action":"say","payload":{},"thought":""}`,
		`{"action":"say","payload":{"text":"a","text":"b"},"thought":""}`,
		`{"action":"","payload":{},"thought":""}`,
		`{"action":"say","payload":{"text":"unclosed"},"thought":""`,
		`no JSON`,
		strings.Repeat("x", llm.MaxOutputBytes+1),
	} {
		if _, err := ParseAction(input); !errors.Is(err, ErrInvalidAction) {
			t.Fatalf("accepted invalid response of length %d: %v", len(input), err)
		}
	}
}

func TestActionValidationAndFallback(t *testing.T) {
	v := engine.View{Phase: engine.PhaseView{Type: "search"}, Allowed: []engine.ActionSpec{
		{Type: "ready"},
		{Type: "publish", Optional: true},
		{Type: "search", Params: map[string]string{"location": "ID"}, Options: []engine.Option{{Value: "a"}, {Value: "b"}}},
	}, Data: map[string]any{"clues": clueView{AP: 2, Locations: []location{{ID: "a", Remaining: 0}, {ID: "b", Remaining: 1}}}}}
	if specs := turnActions(v); len(specs) != 1 || specs[0].Type != "search" {
		t.Fatalf("search did not take priority: %+v", specs)
	}
	d, ok := safeDecision(v, "")
	if !ok || d.Action != "search" || string(d.Payload) != `{"location":"b"}` {
		t.Fatalf("bad search fallback: %+v", d)
	}
	for _, payload := range []string{`{"location":"a"}`, `{"location":"b","thought":"secret"}`, `{"location":5}`} {
		if validateDecision(Decision{Action: "search", Payload: json.RawMessage(payload)}, v) == nil {
			t.Fatalf("accepted invalid search payload %s", payload)
		}
	}
	v.Data["clues"] = clueView{AP: 0}
	d, ok = safeDecision(v, "")
	if !ok || d.Action != "ready" {
		t.Fatalf("expected ready after AP exhaustion: %+v", d)
	}
	v.Allowed = []engine.ActionSpec{{Type: "say", Optional: true}, {Type: "pm.send", Optional: true}, {Type: "publish", Optional: true}}
	if len(turnActions(v)) != 0 {
		t.Fatal("optional actions drive the loop")
	}
}

func assertPromptActionSchema(t *testing.T, prompt string, branches ...string) {
	t.Helper()
	var request struct {
		Schema any `json:"action_schema"`
	}
	if err := json.Unmarshal([]byte(prompt), &request); err != nil {
		t.Fatal(err)
	}
	const dialect = `"$schema":"https://json-schema.org/draft/2020-12/schema"`
	wantJSON := `{` + dialect + `,"not":{}}`
	if len(branches) > 0 {
		wantJSON = `{` + dialect + `,"oneOf":[` + strings.Join(branches, ",") + `]}`
	}
	var want any
	if err := json.Unmarshal([]byte(wantJSON), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(request.Schema, want) {
		t.Fatalf("action_schema = %#v, want %s", request.Schema, wantJSON)
	}
}

func expectedActionBranch(action, payload string) string {
	return `{"type":"object","properties":{"action":{"type":"string","const":"` + action +
		`"},"payload":` + payload + `,"thought":{"type":"string"}},` +
		`"required":["action","payload","thought"],"additionalProperties":false}`
}

func TestActionSchemaPrompt(t *testing.T) {
	for _, tc := range []struct {
		spec    engine.ActionSpec
		payload string
	}{
		{engine.ActionSpec{Type: "ready"}, `{"type":"object","properties":{},"required":[],"additionalProperties":false}`},
		{engine.ActionSpec{Type: "say"}, `{"type":"object","properties":{"text":{"type":"string","minLength":1}},"required":["text"],"additionalProperties":false}`},
		{engine.ActionSpec{Type: "search", Options: []engine.Option{{Value: "library"}}}, `{"type":"object","properties":{"location":{"type":"string","minLength":1}},"required":["location"],"additionalProperties":false}`},
		{engine.ActionSpec{Type: "vote.cast", Options: []engine.Option{{Value: "q1"}}}, `{"type":"object","properties":{"option":{"type":"string","minLength":1},"question":{"type":"string","minLength":1}},"required":["option","question"],"additionalProperties":false}`},
		{engine.ActionSpec{Type: "custom.inspect", Params: map[string]string{"target": "target ID", "item": "item ID"}, Options: []engine.Option{{Value: "not-a-parameter-mapping"}}}, `{"type":"object","properties":{"item":{"type":"string","minLength":1},"target":{"type":"string","minLength":1}},"required":["item","target"],"additionalProperties":false}`},
	} {
		t.Run(tc.spec.Type, func(t *testing.T) {
			v := engine.View{Allowed: []engine.ActionSpec{tc.spec}}
			m := memory{}
			for _, correction := range []string{"", "invalid payload"} {
				prompt, err := m.prompt(v, correction)
				if err != nil {
					t.Fatal(err)
				}
				assertPromptActionSchema(t, prompt, expectedActionBranch(tc.spec.Type, tc.payload))
				var request struct {
					Options []engine.ActionSpec `json:"action_options"`
				}
				if err := json.Unmarshal([]byte(prompt), &request); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(request.Options, v.Allowed) {
					t.Fatalf("action_options changed: %+v", request.Options)
				}
			}
		})
	}
}

func TestActionSchemaPermittedActions(t *testing.T) {
	const emptyPayload = `{"type":"object","properties":{},"required":[],"additionalProperties":false}`
	const textPayload = `{"type":"object","properties":{"text":{"type":"string","minLength":1}},"required":["text"],"additionalProperties":false}`
	for _, tc := range []struct {
		name     string
		view     engine.View
		branches []string
	}{
		{"multiple", engine.View{Allowed: []engine.ActionSpec{{Type: "say"}, {Type: "pass"}, {Type: "pm.send", Optional: true}, {Type: "host.advance"}}}, []string{expectedActionBranch("say", textPayload), expectedActionBranch("pass", emptyPayload)}},
		{"ready priority", engine.View{Phase: engine.PhaseView{Type: "reading"}, Allowed: []engine.ActionSpec{{Type: "say"}, {Type: "ready"}}}, []string{expectedActionBranch("ready", emptyPayload)}},
		{"search priority", engine.View{Allowed: []engine.ActionSpec{{Type: "ready"}, {Type: "search"}}}, []string{expectedActionBranch("search", emptyPayload)}},
		{"empty", engine.View{}, nil},
		{"filtered empty", engine.View{Allowed: []engine.ActionSpec{{Type: "pm.send", Optional: true}, {Type: "host.advance"}}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := memory{}
			prompt, err := m.prompt(tc.view, "")
			if err != nil {
				t.Fatal(err)
			}
			assertPromptActionSchema(t, prompt, tc.branches...)
		})
	}
}

func TestActionSchemaDecisionFields(t *testing.T) {
	v := engine.View{Allowed: []engine.ActionSpec{{Type: "say"}}}
	for _, tc := range []struct {
		name, response string
		valid          bool
	}{
		{"top-level thought", `{"action":"say","payload":{"text":"hello"},"thought":"private"}`, true},
		{"empty thought", `{"action":"say","payload":{"text":"hello"},"thought":""}`, true},
		{"extra top-level key", `{"action":"say","payload":{"text":"hello"},"thought":"private","extra":"no"}`, false},
		{"extra payload key", `{"action":"say","payload":{"text":"hello","extra":"no"},"thought":"private"}`, false},
		{"thought in payload", `{"action":"say","payload":{"text":"hello","thought":"private"},"thought":""}`, false},
		{"missing top-level thought", `{"action":"say","payload":{"text":"hello","thought":"private"}}`, false},
		{"missing payload property", `{"action":"say","payload":{},"thought":""}`, false},
		{"empty payload string", `{"action":"say","payload":{"text":""},"thought":""}`, false},
		{"non-string payload", `{"action":"say","payload":{"text":1},"thought":""}`, false},
		{"non-string thought", `{"action":"say","payload":{"text":"hello"},"thought":1}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := ParseAction(tc.response)
			if err == nil {
				err = validateDecision(d, v)
			}
			if (err == nil) != tc.valid {
				t.Fatalf("valid = %v, want %v: %v", err == nil, tc.valid, err)
			}
		})
	}
}

func TestVoteCastUsesFilteredQuestions(t *testing.T) {
	v := engine.View{Allowed: []engine.ActionSpec{{Type: "vote.cast", Params: map[string]string{"question": "ID", "option": "value"}, Options: []engine.Option{{Value: "q2"}}}},
		Data: map[string]any{"votes": voteView{Questions: []voteQuestion{
			{ID: "q1", Options: []engine.Option{{Value: "other"}}},
			{ID: "q2", Options: []engine.Option{{Value: "first"}, {Value: "second"}}},
		}, Cast: map[string]string{"q1": "other"}}}}
	d, ok := safeDecision(v, "")
	if !ok || string(d.Payload) != `{"option":"first","question":"q2"}` {
		t.Fatalf("bad vote fallback: %+v", d)
	}
	for _, p := range []string{`{"option":"second","question":"q1"}`, `{"option":"unknown","question":"q2"}`} {
		if validateDecision(Decision{Action: "vote.cast", Payload: json.RawMessage(p)}, v) == nil {
			t.Fatalf("accepted invalid vote: %s", p)
		}
	}
}
