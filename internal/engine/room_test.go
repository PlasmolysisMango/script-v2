package engine

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"jbs/internal/script"
)

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	r, _ := NewRegistry()
	r.PhaseType("test", PhaseType{OnEnter: func(c *Ctx) { c.Set("reading.act", 1); c.Set("mutable", map[string]any{"counter": 1}) }, Allowed: func(c *Ctx, pid string) []ActionSpec { return []ActionSpec{{Type: "speak"}, {Type: "finish"}} }})
	r.Action("speak", func(c *Ctx, a Action) error {
		c.Emit("public", a.Actor, Public(), map[string]any{"text": "hello"})
		c.Emit("private", a.Actor, To(a.Actor), map[string]any{"text": "only-" + a.Actor})
		return nil
	})
	r.Action("finish", func(c *Ctx, a Action) error { c.Advance(); return nil })
	r.View(func(c *Ctx, pid string) (string, any) { return "mutable", c.Get("mutable") })
	return r
}
func testPackage() *script.Package {
	return &script.Package{Meta: script.Meta{ID: "test", Title: "Test", Players: 2}, Characters: []script.Character{{ID: "a", Name: "A", Sections: []script.Section{{Act: 1, Body: "secret-alpha"}}}, {ID: "b", Name: "B", Sections: []script.Section{{Act: 1, Body: "secret-beta"}}}}, Flow: []script.Step{{ID: "first", Type: "test"}, {ID: "end", Type: "end"}}}
}
func TestPrivacyStaleAndSnapshot(t *testing.T) {
	r, err := NewRoom("test", testPackage(), testRegistry(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ta, err := r.Join("a", "A", "human")
	if err != nil {
		t.Fatal(err)
	}
	tb, err := r.Join("b", "B", "human")
	if err != nil {
		t.Fatal(err)
	}
	if pid, ok := r.Token(ta); !ok || pid != "a" {
		t.Fatal("token")
	}
	if pid, ok := r.Token(tb); !ok || pid != "b" {
		t.Fatal("token")
	}
	v := r.View("a")
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), "secret-beta") {
		t.Fatal("other script leaked")
	}
	a := NewAction("speak", nil)
	a.Actor = "a"
	a.StateID = v.StateID
	if err = r.Submit(a); err != nil {
		t.Fatal(err)
	}
	if err = r.Submit(a); !errors.Is(err, ErrStale) {
		t.Fatalf("stale not rejected: %v", err)
	}
	log := r.Log("b", 0)
	for _, e := range log {
		if e.Type == "private" {
			t.Fatal("private event leaked")
		}
	}
	snap := r.View("a")
	snap.Data["mutable"].(map[string]any)["counter"] = 9
	v = r.View("a")
	if v.Data["mutable"].(map[string]any)["counter"] != float64(1) {
		t.Fatal("snapshot aliases mutable state")
	}
	a = NewAction("finish", nil)
	a.Actor = "a"
	if err = r.Submit(a); err != nil {
		t.Fatal(err)
	}
	if !r.View("a").Ended {
		t.Fatal("not ended")
	}
}
func TestBeforeActionVeto(t *testing.T) {
	reg := testRegistry(t)
	reg.Hook(Hooks{BeforeAction: func(c *Ctx, a *Action) error { return errors.New("veto") }})
	r, _ := NewRoom("veto", testPackage(), reg, nil)
	defer r.Close()
	r.Join("a", "A", "human")
	before := r.LastSeq()
	a := NewAction("speak", nil)
	a.Actor = "a"
	if err := r.Submit(a); err == nil {
		t.Fatal("veto ignored")
	}
	if r.LastSeq() != before {
		t.Fatal("veto emitted event")
	}
}
