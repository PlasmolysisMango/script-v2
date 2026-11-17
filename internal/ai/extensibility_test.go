package ai

import (
	"encoding/json"
	"strings"
	"testing"

	"jbs/internal/engine"
	"jbs/internal/script"
)

func TestRegisteredPhaseScopedPromptFragments(t *testing.T) {
	r, err := engine.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	r.PhaseType("auction", engine.PhaseType{Allowed: func(*engine.Ctx, string) []engine.ActionSpec {
		return []engine.ActionSpec{{Type: "auction.bid", Params: map[string]string{"amount": "数字字符串"}}}
	}})
	r.Action("auction.bid", func(c *engine.Ctx, _ engine.Action) error { c.Advance(); return nil })
	r.PromptFragment("auction", func(_ *engine.Ctx, pid string) string { return "竞拍规则只给" + pid })
	r.PromptFragment("reading", func(*engine.Ctx, string) string { return "不应出现的阅读规则" })
	p := &script.Package{Meta: script.Meta{ID: "auction", Title: "竞拍", Players: 1}, Characters: []script.Character{{ID: "a", Name: "A"}}, Flow: []script.Step{{ID: "auction", Type: "auction"}, {ID: "end", Type: "end"}}}
	room, err := engine.NewRoom("auction", p, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer room.Close()
	if _, err = room.Join("a", "A", "human"); err != nil {
		t.Fatal(err)
	}
	v := room.View("a")
	m := memory{}
	prompt, err := m.prompt(v, "")
	if err != nil || !strings.Contains(prompt, "竞拍规则只给a") || strings.Contains(prompt, "不应出现") {
		t.Fatal("registered phase fragment was not correctly scoped", err)
	}
	assertPromptActionSchema(t, prompt, expectedActionBranch("auction.bid", `{"type":"object","properties":{"amount":{"type":"string","minLength":1}},"required":["amount"],"additionalProperties":false}`))
	a := engine.NewAction("auction.bid", map[string]string{"amount": "10"})
	a.Actor, a.StateID = "a", v.StateID
	if err = room.Submit(a); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(room.View("a").Hint, "竞拍规则") {
		t.Fatal("fragment leaked into a different phase")
	}
}

func TestPluginActionsAndPromptFragments(t *testing.T) {
	v := engine.View{PID: "a", Phase: engine.PhaseView{ID: "auction", Type: "auction"}, Hint: "竞拍时选择一个数字出价。", Allowed: []engine.ActionSpec{{Type: "auction.bid", Params: map[string]string{"amount": "数字字符串"}}}, Data: map[string]any{"auction": map[string]any{"balance": 20}}}
	acts := turnActions(v)
	if len(acts) != 1 || acts[0].Type != "auction.bid" {
		t.Fatal("custom action filtered")
	}
	d := Decision{Action: "auction.bid", Payload: json.RawMessage(`{"amount":"10"}`)}
	if err := validateDecision(d, v); err != nil {
		t.Fatal(err)
	}
	m := memory{}
	prompt, err := m.prompt(v, "")
	if err != nil || !strings.Contains(prompt, "auction") || !strings.Contains(prompt, "竞拍") {
		t.Fatal("plugin context omitted", err)
	}
}
