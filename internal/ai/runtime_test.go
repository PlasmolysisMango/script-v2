package ai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"jbs/internal/ai/llm"
	"jbs/internal/engine"
	"jbs/internal/plugins"
	"jbs/internal/script"
)

type providerFunc func(context.Context, string, string) (string, error)

func (f providerFunc) Complete(ctx context.Context, system, prompt string) (string, error) {
	return f(ctx, system, prompt)
}

type fakeRoom struct {
	mu       sync.Mutex
	v        engine.View
	events   []engine.Event
	actions  []engine.Action
	notify   chan struct{}
	waiting  chan struct{}
	stale    int
	endAfter int
}

func newFakeRoom(specs ...engine.ActionSpec) *fakeRoom {
	return &fakeRoom{v: engine.View{RoomID: "fake", PID: "p", StateID: 1, LastSeq: 1,
		Phase: engine.PhaseView{ID: "phase", Type: "discussion"}, Allowed: specs, Data: map[string]any{},
		Me: &engine.CharacterView{ID: "p", Name: "角色", Sections: []script.Section{{Act: 1, Body: "own-section-secret"}}, Goals: []string{"own-goal"}, Ability: "own-ability", Performance: "谨慎发言"}},
		notify: make(chan struct{}), waiting: make(chan struct{}, 10), endAfter: 1}
}

func (r *fakeRoom) View(pid string) engine.View {
	if pid != "p" {
		panic("unfiltered view requested")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b, _ := json.Marshal(r.v)
	var v engine.View
	_ = json.Unmarshal(b, &v)
	return v
}
func (r *fakeRoom) Log(pid string, since int64) []engine.Event {
	if pid != "p" {
		panic("unfiltered log requested")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []engine.Event
	for _, e := range r.events {
		if e.Seq > since && e.VisibleTo(pid) {
			out = append(out, e)
		}
	}
	return out
}
func (r *fakeRoom) bump() {
	r.v.StateID++
	r.v.LastSeq++
	close(r.notify)
	r.notify = make(chan struct{})
}
func (r *fakeRoom) change(fn func(*engine.View)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(&r.v)
	r.bump()
}
func (r *fakeRoom) Submit(a engine.Action) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a.Actor != "p" {
		return errors.New("wrong seat")
	}
	if r.stale > 0 {
		r.stale--
		r.bump()
		return engine.ErrStale
	}
	if r.v.Ended {
		return engine.ErrEnded
	}
	if a.StateID != r.v.StateID {
		return engine.ErrStale
	}
	if validateDecision(Decision{Action: a.Type, Payload: a.Payload}, r.v) != nil {
		return engine.ErrNotAllowed
	}
	r.actions = append(r.actions, a)
	if len(r.actions) >= r.endAfter {
		r.v.Ended = true
		r.v.Allowed = nil
	}
	r.bump()
	return nil
}
func (r *fakeRoom) Wait(ctx context.Context, since int64) {
	r.mu.Lock()
	if r.v.LastSeq > since {
		r.mu.Unlock()
		return
	}
	ch := r.notify
	r.mu.Unlock()
	select {
	case r.waiting <- struct{}{}:
	default:
	}
	select {
	case <-ch:
	case <-ctx.Done():
	}
}
func (r *fakeRoom) submitted() []engine.Action {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]engine.Action(nil), r.actions...)
}

func await[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("timed out")
		var zero T
		return zero
	}
}

func saySpecs() []engine.ActionSpec {
	return []engine.ActionSpec{{Type: "say", Params: map[string]string{"text": "speech"}}, {Type: "pass"}, {Type: "pm.send", Optional: true}, {Type: "publish", Optional: true}}
}

func TestReadyWithoutCompletion(t *testing.T) {
	for _, phase := range []string{"reading", "ready_check"} {
		t.Run(phase, func(t *testing.T) {
			r := newFakeRoom(engine.ActionSpec{Type: "ready"}, engine.ActionSpec{Type: "pm.send", Optional: true})
			r.v.Phase.Type = phase
			var calls atomic.Int32
			p := providerFunc(func(context.Context, string, string) (string, error) {
				calls.Add(1)
				return "", errors.New("unexpected")
			})
			results := make(chan Result, 1)
			stop := Start(context.Background(), r, "p", p, Options{OnResult: func(r Result) { results <- r }, Stats: &Stats{}})
			defer stop()
			result := await(t, results)
			if calls.Load() != 0 || !result.Submitted || result.Action != "ready" || result.Fallback {
				t.Fatalf("bad ready result: %+v", result)
			}
		})
	}
}

func TestInvalidRetryAndSafeFallback(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		err         error
		calls       int32
		code        string
	}{
		{"invalid", "```json\nnot JSON", nil, 2, "invalid_action"},
		{"optional", `{"action":"pm.send","payload":{"to":"x","text":"secret"},"thought":"private"}`, nil, 2, "invalid_action"},
		{"error", "", errors.New("secret-token in raw provider error"), 2, "provider_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newFakeRoom(saySpecs()...)
			var calls atomic.Int32
			p := providerFunc(func(ctx context.Context, sys, prompt string) (string, error) {
				n := calls.Add(1)
				if n == 2 && !strings.Contains(prompt, "correction") {
					t.Error("missing retry correction")
				}
				return tc.reply, tc.err
			})
			results := make(chan Result, 1)
			stats := &Stats{}
			stop := Start(context.Background(), r, "p", p, Options{Stats: stats, OnResult: func(r Result) { results <- r }})
			defer stop()
			result := await(t, results)
			if calls.Load() != tc.calls || !result.Fallback || !result.Submitted || result.Action != "pass" || result.Error != tc.code {
				t.Fatalf("bad fallback %+v, calls %d", result, calls.Load())
			}
			b, _ := json.Marshal(result)
			if strings.Contains(string(b), "secret-token") || strings.Contains(string(b), "private") {
				t.Fatal("diagnostic leaked provider data")
			}
			if s := stats.Snapshot(); s.Results != 1 || s.Fallbacks != 1 || s.Errors != 1 || s.Submitted != 1 {
				t.Fatalf("bad stats %+v", s)
			}
		})
	}
}

func TestTransientProviderErrorRecoversWithoutFallback(t *testing.T) {
	r := newFakeRoom(saySpecs()...)
	var calls atomic.Int32
	p := providerFunc(func(context.Context, string, string) (string, error) {
		if calls.Add(1) == 1 {
			return "", errors.New("transient provider failure")
		}
		return `{"action":"say","payload":{"text":"恢复后的证词"},"thought":"继续核对"}`, nil
	})
	results := make(chan Result, 1)
	stop := Start(context.Background(), r, "p", p, Options{OnResult: func(result Result) { results <- result }})
	defer stop()
	result := await(t, results)
	if calls.Load() != 2 || result.Fallback || !result.Submitted || result.Action != "say" {
		t.Fatalf("retry did not recover: %+v, calls=%d", result, calls.Load())
	}
}

func TestRetryExplainsInvalidOutput(t *testing.T) {
	for _, tc := range []struct {
		name, reply, correction string
	}{
		{"json", "not JSON", "JSON结构无效"},
		{"payload", `{"action":"say","payload":{"text":"证词","speaker":"p"},"thought":"怀疑"}`, "动作或payload不合法"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			p := providerFunc(func(_ context.Context, _, prompt string) (string, error) {
				calls++
				if calls == 1 {
					return tc.reply, nil
				}
				if !strings.Contains(prompt, tc.correction) {
					t.Fatal("retry omitted failure category")
				}
				return `{"action":"say","payload":{"text":"修正后的证词"},"thought":"核对"}`, nil
			})
			d, err := generate(context.Background(), p, &memory{}, newFakeRoom(saySpecs()...).v)
			if err != nil || d.Action != "say" || calls != 2 {
				t.Fatalf("retry failed: %+v %v calls=%d", d, err, calls)
			}
		})
	}
}

func TestCompletionRevalidatedWithFreshState(t *testing.T) {
	r := newFakeRoom(saySpecs()...)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	p := providerFunc(func(ctx context.Context, sys, prompt string) (string, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		return `{"action":"say","payload":{"text":"公开发言"},"thought":"private-thought"}`, nil
	})
	results := make(chan Result, 1)
	stop := Start(context.Background(), r, "p", p, Options{OnResult: func(r Result) { results <- r }, Stats: &Stats{}})
	defer stop()
	await(t, started)
	// Room operations remain responsive during generation; an unrelated event
	// advances state without changing this seat's compulsory action.
	r.change(func(v *engine.View) { v.Hint = "another seat acted" })
	close(release)
	result := await(t, results)
	actions := r.submitted()
	if !result.Submitted || calls.Load() != 1 || len(actions) != 1 || actions[0].StateID != 2 {
		t.Fatalf("completion not refreshed: %+v, %+v", result, actions)
	}
	if strings.Contains(string(actions[0].Payload), "private-thought") {
		t.Fatal("thought published")
	}
}

func TestTransientStaleRetriesWithoutRegeneration(t *testing.T) {
	r := newFakeRoom(saySpecs()...)
	r.stale = 2
	var calls atomic.Int32
	p := providerFunc(func(ctx context.Context, sys, prompt string) (string, error) {
		calls.Add(1)
		return (llm.Mock{}).Complete(ctx, sys, prompt)
	})
	results := make(chan Result, 1)
	stop := Start(context.Background(), r, "p", p, Options{OnResult: func(r Result) { results <- r }, Stats: &Stats{}})
	defer stop()
	result := await(t, results)
	if !result.Submitted || calls.Load() != 1 || r.submitted()[0].StateID != 3 {
		t.Fatalf("bad stale retry %+v", result)
	}
}

func TestChangedPhaseDiscardsCompletion(t *testing.T) {
	r := newFakeRoom(saySpecs()...)
	started, release := make(chan struct{}), make(chan struct{})
	p := providerFunc(func(ctx context.Context, sys, prompt string) (string, error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		return `{"action":"say","payload":{"text":"旧阶段发言"},"thought":""}`, nil
	})
	results := make(chan Result, 1)
	stop := Start(context.Background(), r, "p", p, Options{OnResult: func(r Result) { results <- r }, Stats: &Stats{}})
	defer stop()
	await(t, started)
	r.change(func(v *engine.View) { v.Phase.ID = "next"; v.Allowed = nil })
	close(release)
	result := await(t, results)
	if result.Submitted || result.Error != "stale" || len(r.submitted()) != 0 {
		t.Fatalf("submitted stale phase: %+v", result)
	}
	await(t, r.waiting)
}

func TestOptionalOnlyWaitsAndCancellationStopsGeneration(t *testing.T) {
	t.Run("optional", func(t *testing.T) {
		r := newFakeRoom(engine.ActionSpec{Type: "pm.send", Optional: true}, engine.ActionSpec{Type: "publish", Optional: true})
		var calls atomic.Int32
		p := providerFunc(func(context.Context, string, string) (string, error) { calls.Add(1); return "", nil })
		stop := Start(context.Background(), r, "p", p)
		await(t, r.waiting)
		stop()
		if calls.Load() != 0 || len(r.submitted()) != 0 {
			t.Fatal("optional action generated")
		}
	})
	t.Run("cancel", func(t *testing.T) {
		r := newFakeRoom(saySpecs()...)
		started, canceled := make(chan struct{}), make(chan struct{})
		p := providerFunc(func(ctx context.Context, sys, prompt string) (string, error) {
			close(started)
			<-ctx.Done()
			close(canceled)
			return "", ctx.Err()
		})
		stop := Start(context.Background(), r, "p", p)
		await(t, started)
		stop()
		await(t, canceled)
		if len(r.submitted()) != 0 {
			t.Fatal("submitted after cancellation")
		}
	})
}

func TestMemorySummarizationAndPrivateNotes(t *testing.T) {
	m := memory{}
	for i := 0; i < 5; i++ {
		m.history = append(m.history, message{Text: strings.Repeat("字", 1800)})
	}
	var summaryCalls int
	p := providerFunc(func(ctx context.Context, sys, prompt string) (string, error) {
		summaryCalls++
		if !strings.Contains(prompt, `"mode":"summary"`) {
			t.Error("not a summary request")
		}
		return strings.Repeat("摘", 2500), nil
	})
	if err := m.summarize(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if summaryCalls != 1 || len([]rune(m.summary)) != maxSummaryChars || historyChars(m.history) > 3000 {
		t.Fatal("memory not bounded")
	}

	r := newFakeRoom(saySpecs()...)
	r.endAfter = 2
	r.events = []engine.Event{{Seq: 1, Type: "chat.say", Public: true, Payload: map[string]any{"text": "public-chat"}}}
	var calls atomic.Int32
	p = providerFunc(func(ctx context.Context, sys, prompt string) (string, error) {
		n := calls.Add(1)
		if !strings.Contains(prompt, "own-section-secret") || !strings.Contains(prompt, "own-goal") || !strings.Contains(prompt, "own-ability") || !strings.Contains(prompt, "public-chat") {
			t.Error("missing own context")
		}
		if n == 2 && !strings.Contains(prompt, `"notes":"private-notes"`) {
			t.Error("notes were not retained")
		}
		return `{"action":"say","payload":{"text":"角色公开发言"},"thought":"private-notes"}`, nil
	})
	results := make(chan Result, 2)
	stop := Start(context.Background(), r, "p", p, Options{OnResult: func(r Result) { results <- r }, Stats: &Stats{}})
	defer stop()
	await(t, results)
	await(t, results)
	for _, a := range r.submitted() {
		if strings.Contains(string(a.Payload), "private-notes") {
			t.Fatal("notes leaked to action")
		}
	}
}

func TestGenerationRetriesInvalidOnceThenAccepts(t *testing.T) {
	r := newFakeRoom(saySpecs()...)
	var calls int
	p := providerFunc(func(ctx context.Context, sys, prompt string) (string, error) {
		calls++
		if calls == 1 {
			return "invalid JSON", nil
		}
		return "```json\n{\"action\":\"say\",\"payload\":{\"text\":\"有效发言\"},\"thought\":\"私密笔记\"}\n```", nil
	})
	d, err := generate(context.Background(), p, &memory{}, r.View("p"))
	if err != nil || calls != 2 || d.Action != "say" {
		t.Fatalf("retry failed: %+v, %v", d, err)
	}
}

func TestSubmissionRejectsChangedTurnAndLocation(t *testing.T) {
	t.Run("turn", func(t *testing.T) {
		r := newFakeRoom(saySpecs()...)
		r.v.Data["turn"] = map[string]any{"pid": "p", "round": 1}
		initial := r.View("p")
		r.change(func(v *engine.View) { v.Data["turn"] = map[string]any{"pid": "p", "round": 2} })
		d := Decision{Action: "say", Payload: json.RawMessage(`{"text":"旧轮发言"}`)}
		if _, err := submitFresh(context.Background(), r, "p", initial, d); !errors.Is(err, engine.ErrStale) || len(r.submitted()) != 0 {
			t.Fatal("old-turn completion was not rejected")
		}
	})
	t.Run("location", func(t *testing.T) {
		r := newFakeRoom(engine.ActionSpec{Type: "search", Params: map[string]string{"location": "ID"}, Options: []engine.Option{{Value: "a"}, {Value: "b"}}})
		r.v.Data["clues"] = clueView{AP: 2, Locations: []location{{ID: "a", Remaining: 1}, {ID: "b", Remaining: 1}}}
		initial := r.View("p")
		r.change(func(v *engine.View) {
			v.Data["clues"] = clueView{AP: 2, Locations: []location{{ID: "a", Remaining: 0}, {ID: "b", Remaining: 1}}}
		})
		d := Decision{Action: "search", Payload: json.RawMessage(`{"location":"a"}`)}
		if _, err := submitFresh(context.Background(), r, "p", initial, d); !errors.Is(err, engine.ErrStale) || len(r.submitted()) != 0 {
			t.Fatal("unavailable-location completion was not rejected")
		}
	})
}

func TestEngineRoundTripWithTwoAISeats(t *testing.T) {
	reg, err := plugins.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	pkg := &script.Package{Meta: script.Meta{ID: "test", Title: "测试"}, Truth: "answer-key-secret",
		Characters: []script.Character{
			{ID: "a", Name: "甲", Sections: []script.Section{{Act: 1, Body: "a-private-section"}}},
			{ID: "b", Name: "乙", IsCulprit: true, CanLie: true, Sections: []script.Section{{Act: 1, Body: "b-private-section"}}},
		}, Locations: []script.Location{{ID: "loc", Name: "书房", Act: 1}},
		Clues: []script.Clue{{ID: "c1", Location: "loc", Body: "线索一", Act: 1, Visibility: "private"}, {ID: "c2", Location: "loc", Body: "线索二", Act: 1, Visibility: "private"}},
		Votes: []script.Vote{{ID: "culprit", Question: "谁是凶手？", Options: "characters", Correct: "b"}, {ID: "second", Question: "再次投票", Options: "characters", Correct: "b"}},
		Flow: []script.Step{{ID: "read", Type: "reading"}, {ID: "search", Type: "search", Params: map[string]any{"action_points": 1}},
			{ID: "talk", Type: "discussion", Params: map[string]any{"rounds": 1}}, {ID: "check", Type: "ready_check"},
			{ID: "talk2", Type: "discussion", Params: map[string]any{"rounds": 1}}, {ID: "vote", Type: "vote"}, {ID: "end", Type: "end"}}}
	r, err := engine.NewRoom("ai-test", pkg, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, pid := range []string{"a", "b"} {
		if _, err := r.Join(pid, pid, "ai"); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stats := &Stats{}
	for _, pid := range []string{"a", "b"} {
		pid := pid
		p := providerFunc(func(ctx context.Context, sys, prompt string) (string, error) {
			other := "a-private-section"
			if pid == "a" {
				other = "b-private-section"
			}
			if strings.Contains(prompt, other) || strings.Contains(prompt, "answer-key-secret") || strings.Contains(prompt, "is_culprit") || strings.Contains(prompt, `"correct"`) {
				t.Error("hidden script data reached provider")
			}
			var request struct {
				Phase engine.PhaseView
				Notes string
			}
			_ = json.Unmarshal([]byte(prompt), &request)
			if request.Phase.ID == "talk2" && request.Notes == "" {
				t.Error("auto-ready cleared private suspicion notes")
			}
			return (llm.Mock{}).Complete(ctx, sys, prompt)
		})
		defer Start(ctx, r, pid, p, Options{Stats: stats})()
	}
	for {
		v := r.View("a")
		if v.Ended {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("AI game did not finish: %+v", v)
		}
		r.Wait(ctx, v.LastSeq)
	}
	counts := map[string]int{}
	for _, e := range r.Log("a", 0) {
		counts[e.Type]++
		if e.Public {
			b, _ := json.Marshal(e)
			if strings.Contains(string(b), "private-section") || strings.Contains(string(b), "thought") {
				t.Fatal("private reasoning appeared in public log")
			}
		}
		if e.Type == "vote.cast" && e.Str("option") != "a" {
			t.Fatal("mock did not vote for first available option")
		}
	}
	if counts["chat.say"] != 4 || counts["vote.cast"] != 2 || counts["clue.found"] != 1 {
		t.Fatalf("missing mandatory actions: %+v", counts)
	}
	if stats.Snapshot().Fallbacks != 0 {
		t.Fatalf("unexpected fallbacks: %+v", stats.Snapshot())
	}
}
