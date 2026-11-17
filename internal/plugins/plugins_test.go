package plugins_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"jbs/internal/engine"
	"jbs/internal/plugins"
	"jbs/internal/plugins/chat"
	"jbs/internal/plugins/clues"
	"jbs/internal/plugins/reveal"
	"jbs/internal/plugins/vote"
	"jbs/internal/script"
)

// A complete miniature package exercises two acts, phase/clue prerequisites,
// two private vote questions and competing conditional endings.
func miniature(t *testing.T) *script.Package {
	t.Helper()
	p := script.Package{
		Meta: script.Meta{ID: "mini", Title: "Miniature mystery", Players: 2, Lang: "en", Intro: "A mystery."},
		Characters: []script.Character{
			{ID: "a", Name: "Ada", PublicIntro: "Detective", TTS: script.Voice{Hint: "alto", Rate: 1.2, Pitch: 0.8}, Sections: []script.Section{{Act: 1, Title: "A1", Body: "SECRET_A1"}, {Act: 2, Title: "A2", Body: "SECRET_A2"}}},
			{ID: "b", Name: "Ben", PublicIntro: "Witness", TTS: script.Voice{Hint: "tenor", Rate: 0.9, Pitch: 1.1}, Sections: []script.Section{{Act: 1, Title: "B1", Body: "SECRET_B1"}, {Act: 2, Title: "B2", Body: "SECRET_B2"}}},
		},
		Acts:      []script.Act{{ID: 1, Title: "Discovery"}, {ID: 2, Title: "Investigation"}},
		Locations: []script.Location{{ID: "library", Name: "Library", Act: 1}, {ID: "annex", Name: "Annex", Act: 2}},
		Clues: []script.Clue{
			{ID: "public1", Location: "library", Title: "Announcement", Body: "PUBLIC_ONE", Act: 1, Visibility: "public"},
			// The dependent clue precedes its prerequisite in package order.
			{ID: "c2", Location: "library", Title: "Note", Body: "PRIVATE_C2", Act: 1, Visibility: "private", Unlock: &script.Unlock{RequiresClues: []string{"c1"}, RequiresPhase: "intro"}},
			{ID: "c1", Location: "library", Title: "Key", Body: "PRIVATE_C1", Act: 1, Visibility: "private"},
			{ID: "c3", Location: "library", Title: "Ledger", Body: "PRIVATE_C3", Act: 1, Visibility: "private", Unlock: &script.Unlock{RequiresClues: []string{"c2"}, RequiresPhase: "reading2"}},
			{ID: "public2", Location: "annex", Title: "Notice", Body: "PUBLIC_TWO", Act: 2, Visibility: "public", Unlock: &script.Unlock{RequiresClues: []string{"c1"}, RequiresPhase: "reading2"}},
			{ID: "c4", Location: "annex", Title: "Receipt", Body: "PRIVATE_C4", Act: 2, Visibility: "private"},
		},
		Flow: []script.Step{
			{ID: "waiting", Type: "ready_check", Title: "Join and ready"},
			{ID: "reading1", Type: "reading", Title: "Act one", Params: map[string]any{"act": 1, "narration": "The doors open."}},
			{ID: "intro", Type: "intro", Title: "Introductions", Params: map[string]any{"rounds": 2}},
			{ID: "discussion", Type: "discussion", Title: "Discussion", Params: map[string]any{"rounds": 2}},
			{ID: "search1", Type: "search", Title: "Search", Params: map[string]any{"act": 1, "action_points": 3}},
			{ID: "reading2", Type: "reading", Title: "Act two", Params: map[string]any{"act": 2}},
			{ID: "search2", Type: "search", Title: "Search again", Params: map[string]any{"act": 2, "action_points": 3}},
			{ID: "vote", Type: "vote", Title: "Vote"},
			{ID: "reveal", Type: "reveal", Title: "Solution"},
			{ID: "end", Type: "end", Title: "Finished"},
		},
		Votes: []script.Vote{{ID: "culprit", Question: "Who did it?", Options: "characters", Correct: "a"}, {ID: "witness", Question: "Who witnessed it?", Options: "characters", Correct: "b"}},
		Endings: []script.Ending{
			{When: map[string]string{"culprit": "*"}, Title: "Fallback", Text: "The mystery remains."},
			{When: map[string]string{"culprit": "correct", "witness": "correct"}, Title: "Solved", Text: "Justice is served."},
			{When: map[string]string{"culprit": "wrong"}, Title: "Mistaken", Text: "The culprit escapes."},
		},
		Truth: "Ada was the culprit.",
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := script.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func newRoom(t *testing.T, p *script.Package) *engine.Room {
	t.Helper()
	r, err := plugins.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	room, err := engine.NewRoom(engine.NewToken(), p, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(room.Close)
	return room
}

func join(t *testing.T, r *engine.Room, pid string) {
	t.Helper()
	if _, err := r.Join(pid, pid, "human"); err != nil {
		t.Fatal(err)
	}
}

func submit(t *testing.T, r *engine.Room, pid, typ string, payload any) {
	t.Helper()
	a := engine.NewAction(typ, payload)
	a.Actor = pid
	if err := r.Submit(a); err != nil {
		t.Fatalf("%s %s: %v", pid, typ, err)
	}
}

func roomAt(t *testing.T, p *script.Package, id string) *engine.Room {
	t.Helper()
	r := newRoom(t, p)
	join(t, r, "a")
	join(t, r, "b")
	for range 20 {
		if r.View("a").Phase.ID == id {
			return r
		}
		submit(t, r, engine.HostID, "host.advance", nil)
	}
	t.Fatalf("did not reach phase %s", id)
	return nil
}

func actionError(r *engine.Room, pid, typ string, payload any) error {
	a := engine.NewAction(typ, payload)
	a.Actor = pid
	return r.Submit(a)
}

func has(v engine.View, typ string) bool {
	return slices.ContainsFunc(v.Allowed, func(s engine.ActionSpec) bool { return s.Type == typ })
}

func events(r *engine.Room, pid, typ string) []engine.Event {
	return slices.DeleteFunc(r.Log(pid, 0), func(e engine.Event) bool { return e.Type != typ })
}

func encoded(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// View data is a JSON contract; engines may return typed values or JSON snapshots.
func data[T any](t *testing.T, v any) T {
	t.Helper()
	var out T
	if err := json.Unmarshal([]byte(encoded(t, v)), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestReadyReadingPrivacyAndHost(t *testing.T) {
	r := newRoom(t, miniature(t))
	if len(r.View("a").Allowed) != 0 || !has(r.View(engine.HostID), "host.advance") {
		t.Fatal("unjoined player or host allowed actions are wrong")
	}
	join(t, r, "a")
	submit(t, r, "a", "ready", nil)
	if r.View("a").Phase.ID != "waiting" {
		t.Fatal("advanced without all players joined")
	}
	if err := actionError(r, "a", "host.advance", nil); !errors.Is(err, engine.ErrNotAllowed) {
		t.Fatalf("player host advance: %v", err)
	}
	join(t, r, "b")
	submit(t, r, "b", "ready", nil)
	v := r.View("a")
	if v.Phase.ID != "reading1" || v.Seats[0].Ready || len(v.Me.Sections) != 1 || v.Me.Sections[0].Body != "SECRET_A1" {
		t.Fatalf("bad reading state: %+v", v)
	}
	for _, pid := range []string{"a", "b", engine.HostID} {
		private := "SECRET_B"
		if pid == "b" {
			private = "SECRET_A"
		}
		if strings.Contains(encoded(t, r.View(pid)), private) || strings.Contains(encoded(t, r.Log(pid, 0)), private) {
			t.Fatalf("%s sees another script", pid)
		}
	}
	sections := events(r, "a", "script.section")
	if len(sections) != 1 || sections[0].Public || !reflect.DeepEqual(sections[0].To, []string{"a"}) {
		t.Fatal("sections must be private to their character")
	}
	if narration := events(r, engine.HostID, "narration"); len(narration) != 1 || narration[0].Str("text") != "The doors open." || !narration[0].Public {
		t.Fatal("missing public narration")
	}
	if got := data[clues.View](t, r.View("a").Data["clues"]).Public; len(got) != 1 || got[0].ID != "public1" {
		t.Fatal("public clues not published on reading entry")
	}
	submit(t, r, "a", "ready", nil)
	if err := actionError(r, "a", "ready", nil); !errors.Is(err, engine.ErrNotAllowed) {
		t.Fatal("duplicate ready accepted")
	}
	submit(t, r, "b", "ready", nil)
	if r.View("a").Phase.ID != "intro" {
		t.Fatal("reading did not finish")
	}
	// Second-act sections unlock on reading entry.
	submit(t, r, engine.HostID, "host.advance", nil)
	submit(t, r, engine.HostID, "host.advance", nil)
	submit(t, r, engine.HostID, "host.advance", nil)
	if v := r.View("a"); v.Phase.ID != "reading2" || len(v.Me.Sections) != 2 {
		t.Fatal("second act did not unlock")
	}
}

func TestRoundRobinChatAndTextValidation(t *testing.T) {
	r := roomAt(t, miniature(t), "intro")
	before := r.View("a").StateID
	if err := actionError(r, "b", "say", map[string]any{"text": "Not my turn"}); !errors.Is(err, engine.ErrNotAllowed) {
		t.Fatalf("out of turn say: %v", err)
	}
	for _, payload := range []any{map[string]any{"text": strings.Repeat("界", 4001)}, map[string]any{"text": " \n\t"}, map[string]any{"text": 1}} {
		if err := actionError(r, "a", "say", payload); err == nil {
			t.Fatal("bad text accepted")
		}
	}
	a := engine.Action{Type: "say", Actor: "a", Payload: json.RawMessage(`{"text":`)}
	if err := r.Submit(a); err == nil || r.View("a").StateID != before {
		t.Fatal("malformed input changed state")
	}
	for i, pid := range []string{"a", "b", "a", "b"} {
		turn := data[chat.Turn](t, r.View(pid).Data["turn"])
		if turn.PID != pid || turn.Round != i/2+1 || turn.TotalRounds != 2 || !has(r.View(pid), "say") {
			t.Fatalf("bad turn: %+v", turn)
		}
		if i == 0 {
			submit(t, r, pid, "say", map[string]any{"text": strings.Repeat("界", 4000)})
		} else {
			submit(t, r, pid, "pass", nil)
		}
	}
	if r.View("a").Phase.ID != "discussion" {
		t.Fatal("intro did not end after two rounds")
	}
	starts := events(r, engine.HostID, "turn.start")
	if len(starts) != 5 || starts[0].Str("pid") != "a" || starts[3].Str("pid") != "b" {
		t.Fatal("wrong turn.start sequence")
	}
	stale := engine.NewAction("say", map[string]any{"text": "Stale"})
	stale.Actor, stale.StateID = "a", before
	if err := r.Submit(stale); !errors.Is(err, engine.ErrStale) {
		t.Fatalf("stale action: %v", err)
	}
	for _, pid := range []string{"a", "b", "a", "b"} {
		submit(t, r, pid, "say", map[string]any{"text": "Discussion"})
	}
	if r.View("a").Phase.ID != "search1" {
		t.Fatal("discussion did not finish")
	}
}

func TestFreeChatAndPrivateMessages(t *testing.T) {
	p := miniature(t)
	p.Flow[p.StepIndex("discussion")].Params["free"] = true
	r := roomAt(t, p, "discussion")
	if !has(r.View("b"), "say") || !has(r.View("b"), "ready") || has(r.View("b"), "pass") {
		t.Fatal("bad free-chat actions")
	}
	submit(t, r, "b", "say", map[string]any{"text": "Speak freely"})
	for _, to := range []string{"", "missing", engine.HostID, "a"} {
		if err := actionError(r, "a", "pm.send", map[string]any{"to": to, "text": "Secret"}); err == nil {
			t.Fatalf("invalid PM target %q accepted", to)
		}
	}
	if err := actionError(r, "a", "pm.send", map[string]any{"to": "b", "text": strings.Repeat("界", 4001)}); err == nil {
		t.Fatal("oversized PM accepted")
	}
	if err := r.Submit(engine.Action{Actor: "a", Type: "pm.send", Payload: json.RawMessage(`{`)}); err == nil {
		t.Fatal("malformed PM accepted")
	}
	submit(t, r, "a", "pm.send", map[string]any{"to": "b", "text": "SECRET_PM"})
	pm := events(r, "a", "pm.send")
	if len(pm) != 1 || pm[0].Public || !reflect.DeepEqual(pm[0].To, []string{"a", "b"}) || len(events(r, "b", "pm.send")) != 1 || len(events(r, engine.HostID, "pm.send")) != 0 {
		t.Fatal("PM audience leaked")
	}
	if has(r.View(engine.HostID), "pm.send") || !strings.Contains(r.View("a").Hint, "private message") {
		t.Fatal("wrong global PM actions or hint")
	}
	if !slices.ContainsFunc(r.View("a").Panels, func(panel engine.Panel) bool { return panel.ID == "privatechat" && panel.DataKey == "privatechat" }) {
		t.Fatal("missing privatechat panel")
	}
	submit(t, r, "a", "ready", nil)
	submit(t, r, "b", "ready", nil)
	if r.View("a").Phase.ID != "search1" || !has(r.View("a"), "pm.send") {
		t.Fatal("free chat did not complete or PM unavailable in search")
	}
	submit(t, r, engine.HostID, "host.advance", nil)
	if has(r.View("a"), "pm.send") {
		t.Fatal("PM allowed outside discussion/search")
	}
	// Hosts can skip the join barrier; a not-yet-joined target stays invalid.
	r2 := newRoom(t, miniature(t))
	join(t, r2, "a")
	for r2.View("a").Phase.ID != "discussion" {
		submit(t, r2, engine.HostID, "host.advance", nil)
	}
	if err := actionError(r2, "a", "pm.send", map[string]any{"to": "b", "text": "Hello"}); !errors.Is(err, engine.ErrNotAllowed) {
		t.Fatalf("unjoined PM target: %v", err)
	}
}

func TestCluesPrivateOwnershipPrerequisitesAndExhaustion(t *testing.T) {
	r := roomAt(t, miniature(t), "search1")
	v := data[clues.View](t, r.View("a").Data["clues"])
	if v.AP != 3 || len(v.Locations) != 1 || v.Locations[0].Remaining != 1 || len(v.Own) != 0 {
		t.Fatalf("wrong initial clues: %+v", v)
	}
	before := r.View("a").StateID
	if err := actionError(r, "a", "search", map[string]any{"location": "annex"}); err == nil || r.View("a").StateID != before {
		t.Fatal("locked location consumed AP or accepted")
	}
	if err := r.Submit(engine.Action{Actor: "a", Type: "search", Payload: json.RawMessage(`{`)}); err == nil {
		t.Fatal("malformed search accepted")
	}
	submit(t, r, "a", "search", map[string]any{"location": "library"})
	own := data[clues.View](t, r.View("a").Data["clues"])
	other := data[clues.View](t, r.View("b").Data["clues"])
	if own.AP != 2 || len(own.Own) != 1 || own.Own[0].ID != "c1" || len(other.Own) != 0 || other.AP != 3 || other.Locations[0].Remaining != 1 {
		t.Fatal("ownership, AP or global prerequisite incorrect")
	}
	if strings.Contains(encoded(t, r.View("b")), "PRIVATE_C1") || len(events(r, "b", "clue.found")) != 0 || len(events(r, engine.HostID, "clue.found")) != 0 {
		t.Fatal("private clue leaked")
	}
	if err := actionError(r, "a", "publish", map[string]any{"clue": "c2"}); err == nil {
		t.Fatal("published someone else's/unowned clue")
	}
	submit(t, r, "a", "publish", map[string]any{"clue": "c1"})
	if len(data[clues.View](t, r.View("b").Data["clues"]).Public) != 2 || len(events(r, engine.HostID, "clue.published")) != 1 {
		t.Fatal("published clue not public")
	}
	if err := actionError(r, "a", "publish", map[string]any{"clue": "c1"}); !errors.Is(err, engine.ErrNotAllowed) {
		t.Fatal("republication accepted")
	}
	// c2 unlocks globally even though only a discovered c1; c3 still requires reading2.
	submit(t, r, "b", "search", map[string]any{"location": "library"})
	if r.View("a").Phase.ID != "reading2" {
		t.Fatal("exhausted search did not auto-ready with AP remaining")
	}
	v = data[clues.View](t, r.View("b").Data["clues"])
	if len(v.Own) != 1 || v.Own[0].ID != "c2" || len(v.Public) != 3 || len(v.Locations) != 2 || v.Locations[0].Remaining != 1 {
		t.Fatalf("second-act unlocks/public clues wrong: %+v", v)
	}
	if strings.Contains(encoded(t, r.View("a")), "PRIVATE_C2") {
		t.Fatal("other owner's clue leaked in persistent view")
	}
	submit(t, r, "a", "ready", nil)
	submit(t, r, "b", "ready", nil)
	if r.View("a").Phase.ID != "search2" || data[clues.View](t, r.View("a").Data["clues"]).AP != 3 {
		t.Fatal("AP was not reset for new search")
	}
}

func TestSearchEarlyReadyAndAPExhaustion(t *testing.T) {
	for _, ap := range []int{0, 1, 3} {
		t.Run(string(rune('0'+ap)), func(t *testing.T) {
			p := miniature(t)
			p.Flow[p.StepIndex("search1")].Params["action_points"] = ap
			if ap == 0 {
				r := roomAt(t, p, "discussion")
				submit(t, r, engine.HostID, "host.advance", nil)
				if r.View("a").Phase.ID != "reading2" {
					t.Fatal("zero AP search not completed")
				}
				return
			}
			r := roomAt(t, p, "search1")
			if ap == 3 {
				submit(t, r, "a", "ready", nil)
				if !r.View("a").Seats[0].Ready || data[clues.View](t, r.View("a").Data["clues"]).AP != 3 || has(r.View("a"), "search") {
					t.Fatal("early ready with AP left failed")
				}
				submit(t, r, "b", "ready", nil)
			} else {
				submit(t, r, "a", "search", map[string]any{"location": "library"})
				if !r.View("a").Seats[0].Ready || data[clues.View](t, r.View("a").Data["clues"]).AP != 0 {
					t.Fatal("zero AP player not auto-ready")
				}
				submit(t, r, "b", "search", map[string]any{"location": "library"})
			}
			if r.View("a").Phase.ID != "reading2" {
				t.Fatal("search did not complete")
			}
		})
	}
}

func TestVotesPrivateUntilAllQuestionsCompleteAndReveal(t *testing.T) {
	r := roomAt(t, miniature(t), "vote")
	initial := r.View("a").Data["votes"].(map[string]any)
	if _, exists := initial["results"]; exists || strings.Contains(encoded(t, initial), "correct") {
		t.Fatal("vote answers/results leaked before completion")
	}
	questions := data[[]vote.Question](t, initial["questions"])
	if len(questions) != 2 || len(questions[0].Options) != 2 || questions[0].Options[0].Value != "a" {
		t.Fatal("wrong question/option shape")
	}
	for _, payload := range []any{map[string]any{"question": "bad", "option": "a"}, map[string]any{"question": "culprit", "option": "bad"}} {
		if err := actionError(r, "a", "vote.cast", payload); err == nil {
			t.Fatal("invalid vote accepted")
		}
	}
	if err := r.Submit(engine.Action{Actor: "a", Type: "vote.cast", Payload: json.RawMessage(`{`)}); err == nil {
		t.Fatal("malformed vote accepted")
	}
	submit(t, r, "a", "vote.cast", map[string]any{"question": "culprit", "option": "a"})
	if err := actionError(r, "a", "vote.cast", map[string]any{"question": "culprit", "option": "b"}); err == nil {
		t.Fatal("duplicate vote accepted")
	}
	submit(t, r, "a", "vote.cast", map[string]any{"question": "witness", "option": "b"})
	rawOwn := r.View("a").Data["votes"].(map[string]any)["cast"]
	own := data[map[string]string](t, rawOwn)
	if len(own) != 2 || len(data[map[string]string](t, r.View("b").Data["votes"].(map[string]any)["cast"])) != 0 || len(events(r, engine.HostID, "vote.cast")) != 0 {
		t.Fatal("own casts or private vote audience incorrect")
	}
	switch raw := rawOwn.(type) {
	case map[string]string:
		raw["culprit"] = "b"
	case map[string]any:
		raw["culprit"] = "b"
	default:
		t.Fatal("unexpected cast map type")
	}
	if data[map[string]string](t, r.View("a").Data["votes"].(map[string]any)["cast"])["culprit"] != "a" {
		t.Fatal("view cast map shares mutable state")
	}
	submit(t, r, "b", "vote.cast", map[string]any{"question": "culprit", "option": "a"})
	if r.View("a").Phase.ID != "vote" || len(events(r, engine.HostID, "vote.result")) != 0 {
		t.Fatal("results emitted before all questions complete")
	}
	submit(t, r, "b", "vote.cast", map[string]any{"question": "witness", "option": "b"})
	v := r.View("a")
	if !v.Ended || v.Phase.ID != "end" || len(v.Allowed) != 0 {
		t.Fatal("reveal did not auto-advance to end")
	}
	rawResults := v.Data["votes"].(map[string]any)["results"]
	results := data[[]vote.Result](t, rawResults)
	if len(results) != 2 || results[0].Winner != "a" || results[0].Counts["a"] != 2 || !results[0].Correct || !results[1].Correct {
		t.Fatalf("wrong results: %+v", results)
	}
	switch raw := rawResults.(type) {
	case []vote.Result:
		raw[0].Counts["a"] = 100
	case []any:
		raw[0].(map[string]any)["counts"].(map[string]any)["a"] = float64(100)
	default:
		t.Fatal("unexpected result array type")
	}
	if data[[]vote.Result](t, r.View("a").Data["votes"].(map[string]any)["results"])[0].Counts["a"] != 2 {
		t.Fatal("result view shares mutable counts")
	}
	ending := data[reveal.View](t, v.Data["reveal"])
	if ending.Title != "Solved" || ending.Text != "Justice is served." || ending.Truth != r.Script().Truth || len(ending.Results) != 2 {
		t.Fatalf("wrong persistent reveal: %+v", ending)
	}
	if len(events(r, engine.HostID, "vote.result")) != 2 || len(events(r, engine.HostID, "reveal")) != 1 {
		t.Fatal("result/reveal events missing or repeated")
	}
	for _, e := range events(r, "*", "vote.cast") {
		if e.Public || !reflect.DeepEqual(e.To, []string{e.Actor}) {
			t.Fatal("vote choices visible to another player")
		}
	}
}

func TestRevealOmitsEndingTextDuplicateOfTruth(t *testing.T) {
	p := miniature(t)
	p.Truth = "SAME_BODY"
	for i := range p.Endings {
		p.Endings[i].Text = p.Truth
	}
	r := roomAt(t, p, "vote")
	for _, pid := range []string{"a", "b"} {
		submit(t, r, pid, "vote.cast", map[string]any{"question": "culprit", "option": "a"})
		submit(t, r, pid, "vote.cast", map[string]any{"question": "witness", "option": "b"})
	}
	v := r.View("a")
	ending := data[reveal.View](t, v.Data["reveal"])
	if ending.Title != "Solved" || ending.Truth != p.Truth || ending.Text != "" {
		t.Fatalf("duplicate ending text not omitted: %+v", ending)
	}
	raw := events(r, engine.HostID, "reveal")
	if len(raw) != 1 || raw[0].Payload["text"] != "" || raw[0].Payload["truth"] != p.Truth {
		t.Fatalf("reveal event duplicates truth: %+v", raw)
	}
}

func TestVoteTiesAndRevealSkipsToEnd(t *testing.T) {	p := miniature(t)
	p.Flow = append(p.Flow[:p.StepIndex("end")], script.Step{ID: "trap", Type: "discussion", Title: "Should be skipped"}, script.Step{ID: "end", Type: "end", Title: "End"})
	r := roomAt(t, p, "vote")
	for _, pid := range []string{"a", "b"} {
		for _, q := range []string{"culprit", "witness"} {
			submit(t, r, pid, "vote.cast", map[string]any{"question": q, "option": pid})
		}
	}
	v := r.View("b")
	for _, result := range data[[]vote.Result](t, v.Data["votes"].(map[string]any)["results"]) {
		if result.Winner != "" || result.Correct || result.Counts["a"] != 1 || result.Counts["b"] != 1 {
			t.Fatalf("tie must have no winner: %+v", result)
		}
	}
	if v.Phase.ID != "end" || data[reveal.View](t, v.Data["reveal"]).Title != "Mistaken" {
		t.Fatal("wrong ending or reveal did not skip to end")
	}
}

func TestTimerDeadlineAndStaleScheduleGuard(t *testing.T) {
	p := miniature(t)
	p.Flow[0].Params = map[string]any{"timeout": 0.02}
	p.Flow[1].Params["timeout"] = 0.1
	r := newRoom(t, p)
	if r.View(engine.HostID).Phase.Deadline.IsZero() {
		t.Fatal("timeout phase has no deadline")
	}
	sub := r.Subscribe(engine.HostID, 100)
	defer sub.Close()
	submit(t, r, engine.HostID, "host.advance", nil)
	deadline := r.View(engine.HostID).Phase.Deadline
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for {
		select {
		case msg := <-sub.C:
			if msg.Event == nil || msg.Event.Type != "phase.timeout" {
				continue
			}
			if msg.Event.Str("id") != "reading1" || msg.Event.At.Before(deadline) {
				t.Fatal("old phase timer fired in the new phase")
			}
			if v := r.View(engine.HostID); v.Phase.ID != "intro" || !v.Phase.Deadline.IsZero() {
				t.Fatal("timeout did not advance or deadline was not reset")
			}
			return
		case <-ctx.Done():
			t.Fatal("timer never fired")
		}
	}
}

func TestTimerDoesNotRunAfterGameEnd(t *testing.T) {
	p := miniature(t)
	p.Flow[0].Params = map[string]any{"timeout": 0.02}
	p.Flow[0].Next = "reveal"
	r := newRoom(t, p)
	submit(t, r, engine.HostID, "host.advance", nil)
	if !r.View(engine.HostID).Ended {
		t.Fatal("expected ended game")
	}
	seq := r.LastSeq()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	r.Wait(ctx, seq)
	if r.LastSeq() != seq {
		t.Fatal("scheduled callback emitted after game end")
	}
}

type probe struct{}

func (probe) Name() string { return "probe" }
func (probe) Init(r *engine.Registry) error {
	r.PhaseType("probe", engine.PhaseType{OnEnter: func(c *engine.Ctx) {
		c.Emit("narration", "a", engine.To("a"), map[string]any{"text": "Private narration"})
		c.Emit("chat.say", "b", engine.Audience{Public: true, To: []string{"a", "b"}}, map[string]any{"text": "Public speech"})
		c.Emit("reveal", "", engine.To("a", "b"), map[string]any{"truth": "Truth", "text": "Ending"})
		c.Goto("end")
	}})
	return nil
}

func TestTTSExactAudienceVoiceAndNoRecursion(t *testing.T) {
	p := miniature(t)
	p.Flow = append([]script.Step{{ID: "probe", Type: "probe", Title: "Probe"}}, p.Flow...)
	r, err := plugins.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if err := (probe{}).Init(r); err != nil {
		t.Fatal(err)
	}
	room, err := engine.NewRoom(engine.NewToken(), p, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer room.Close()
	all := room.Log("*", 0)
	tts := events(room, "*", "tts")
	if len(tts) != 3 {
		t.Fatalf("expected one tts per source, got %d", len(tts))
	}
	for _, e := range tts {
		seq := data[int64](t, e.Payload["source_seq"])
		source := all[seq-1]
		if e.Public != source.Public || !reflect.DeepEqual(e.To, source.To) || e.Actor != source.Actor {
			t.Fatal("tts did not copy the exact audience and actor")
		}
		if ch := p.Character(e.Actor); ch != nil {
			if e.Str("voice_hint") != ch.TTS.Hint || e.Payload["rate"] != ch.TTS.Rate || e.Payload["pitch"] != ch.TTS.Pitch {
				t.Fatal("actor voice fields not used")
			}
		} else if e.Str("voice_hint") != "narrator" || e.Str("text") != "Truth\n\nEnding" {
			t.Fatal("reveal narrator fields wrong")
		}
	}
	if len(events(room, engine.HostID, "tts")) != 1 || len(events(room, "b", "tts")) != 2 {
		t.Fatal("private tts leaked")
	}
}
