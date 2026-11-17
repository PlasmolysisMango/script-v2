// Package ai drives AI seats using only their filtered engine views and logs.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"jbs/internal/ai/llm"
	"jbs/internal/engine"
)

// Room is implemented by *engine.Room. It intentionally excludes Script and
// other privileged APIs; it also permits deterministic fake-room tests.
type Room interface {
	View(pid string) engine.View
	Log(pid string, since int64) []engine.Event
	Submit(engine.Action) error
	Wait(ctx context.Context, since int64)
}

type Options struct {
	// OnResult executes in the seat loop, never in the room actor. It is private
	// diagnostics only. Callbacks should return promptly and must not broadcast.
	OnResult func(Result)
	// Nil uses DefaultStats. Supply a private Stats instance for room/seat totals.
	Stats *Stats
}

// Start launches one sequential generation loop for a joined AI seat and
// returns its cancellation function. The caller must start at most one loop per
// seat and stop it when the room is removed. Nil providers default to Qoder.
func Start(ctx context.Context, room Room, pid string, provider llm.Provider, options ...Options) context.CancelFunc {
	ctx, stop := context.WithCancel(ctx)
	var opts Options
	if len(options) > 0 {
		opts = options[0]
	}
	if opts.Stats == nil {
		opts.Stats = DefaultStats
	}
	if provider == nil {
		provider = &llm.Qoder{}
	}
	go run(ctx, room, pid, provider, opts)
	return stop
}

func run(ctx context.Context, room Room, pid string, provider llm.Provider, opts Options) {
	if room == nil || pid == "" || pid == "*" || pid == engine.HostID {
		return
	}
	var mem memory
	for ctx.Err() == nil {
		v, err := snapshot(room, pid)
		if err != nil {
			report(opts, Result{PID: pid, Provider: llm.Name(provider), Error: "invalid_view"})
			return
		}
		if v.Ended {
			return
		}
		mem.ingest(room.Log(pid, mem.since), v.LastSeq)
		if len(turnActions(v)) == 0 {
			room.Wait(ctx, v.LastSeq)
			continue
		}
		started := time.Now()
		result := Result{RoomID: v.RoomID, PID: pid, Provider: llm.Name(provider)}
		var d Decision
		if _, ready := findSpec(turnActions(v), "ready"); ready {
			d, _ = safeDecision(v, "ready")
		} else {
			if err := mem.summarize(ctx, provider); err != nil {
				result.Error = llm.ErrorCode(err)
			}
			d, err = generate(ctx, provider, &mem, v)
			if err != nil && ctx.Err() == nil {
				result.Fallback = true
				result.Error = generationError(err)
				var ok bool
				d, ok = safeDecision(v, "")
				if !ok {
					result.DurationMS = time.Since(started).Milliseconds()
					report(opts, result)
					room.Wait(ctx, v.LastSeq)
					continue
				}
			}
		}
		if ctx.Err() != nil {
			return
		}
		result.Action = d.Action
		final, submitErr := submitFresh(ctx, room, pid, v, d)
		if submitErr != nil && !errors.Is(submitErr, engine.ErrEnded) && !errors.Is(submitErr, engine.ErrStale) &&
			!errors.Is(submitErr, engine.ErrNotAllowed) && ctx.Err() == nil {
			// If a handler rejects a generated payload, attempt one safe compulsory
			// action, still subject to fresh-state and phase validation.
			if fallback, ok := safeDecision(final, ""); ok && !sameDecision(fallback, d) {
				result.Fallback = true
				result.Error = "action_rejected"
				result.Action = fallback.Action
				final, submitErr = submitFresh(ctx, room, pid, v, fallback)
			}
		}
		if ctx.Err() != nil {
			return
		}
		result.Submitted = submitErr == nil
		if submitErr != nil && result.Error == "" {
			result.Error = submissionError(submitErr)
		}
		if result.Submitted && !result.Fallback && d.Thought != "" {
			mem.notes = clip(d.Thought, maxNotesChars)
		}
		result.DurationMS = time.Since(started).Milliseconds()
		report(opts, result)
		if final.Ended || errors.Is(submitErr, engine.ErrEnded) {
			return
		}
		// Successful progress should produce a new event. If an action fails at
		// the same state, wait instead of regenerating in a tight loop. A stale
		// state is already progress and is retried on the next loop iteration.
		if submitErr != nil && !errors.Is(submitErr, engine.ErrStale) {
			room.Wait(ctx, final.LastSeq)
		} else if submitErr == nil {
			room.Wait(ctx, final.LastSeq)
		}
	}
}

func generate(ctx context.Context, p llm.Provider, m *memory, v engine.View) (Decision, error) {
	system, err := render("system.tmpl", nil)
	if err != nil {
		return Decision{}, ErrInvalidAction
	}
	correction := ""
	for attempt := 0; attempt < 2; attempt++ {
		prompt, err := m.prompt(v, correction)
		if err != nil {
			return Decision{}, err
		}
		text, err := complete(ctx, p, system, prompt)
		if err != nil {
			if ctx.Err() != nil {
				return Decision{}, ctx.Err()
			}
			if attempt == 0 {
				correction = "上次调用失败（" + llm.ErrorCode(err) + "），请重新返回合法的动作JSON。"
				continue
			}
			return Decision{}, err
		}
		d, err := ParseAction(text)
		if err != nil {
			correction = `上次回答的JSON结构无效。只返回恰好包含action、payload、thought的对象；payload必须为对象，thought必须为字符串。例如：{"action":"say","payload":{"text":"公开证词"},"thought":"简短怀疑笔记"}。`
		} else {
			err = validateDecision(d, v)
			if err != nil {
				correction = "上次动作或payload不合法。动作必须来自action_options，payload只能包含该动作params列出的键，值必须是非空字符串；有options时必须使用选项值而非名称。不要增加speaker、round或thought等payload键。"
			}
		}
		if err == nil {
			return d, nil
		}
		if ctx.Err() != nil {
			return Decision{}, ctx.Err()
		}
	}
	return Decision{}, ErrInvalidAction
}

func snapshot(room Room, pid string) (engine.View, error) {
	// Marshal immediately, so no plugin-owned references are kept during LLM
	// generation. The engine must also snapshot inside its actor to eliminate
	// the race between View returning and this marshal beginning.
	b, err := json.Marshal(room.View(pid))
	var v engine.View
	if err != nil || json.Unmarshal(b, &v) != nil || v.PID != pid {
		return v, ErrInvalidAction
	}
	return v, nil
}

func samePhase(a, b engine.View) bool {
	return a.Phase.ID == b.Phase.ID && a.Phase.Index == b.Phase.Index &&
		a.Phase.Type == b.Phase.Type && a.Phase.Act == b.Phase.Act
}

// submitFresh reuses a completion across unrelated state changes, but never
// crosses a phase/turn boundary or submits a no-longer-allowed action.
func submitFresh(ctx context.Context, room Room, pid string, initial engine.View, d Decision) (engine.View, error) {
	v := initial
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return v, err
		}
		fresh, err := snapshot(room, pid)
		if err != nil {
			return v, err
		}
		v = fresh
		if v.Ended {
			return v, engine.ErrEnded
		}
		if !samePhase(initial, v) {
			return v, engine.ErrStale
		}
		// A turn can change within the same phase (e.g. a timer advanced a
		// discussion round while the completion was pending).
		if !sameTurn(initial, v) {
			return v, engine.ErrStale
		}
		if _, ok := findSpec(turnActions(v), d.Action); !ok {
			return v, engine.ErrNotAllowed
		}
		if validateDecision(d, v) != nil {
			// Options/location availability changed. Regenerate from the fresh
			// view instead of silently submitting a different model choice.
			return v, engine.ErrStale
		}
		a := engine.Action{Type: d.Action, Actor: pid, StateID: v.StateID, Payload: d.Payload}
		if err := ctx.Err(); err != nil {
			return v, err
		}
		err = room.Submit(a)
		if !errors.Is(err, engine.ErrStale) {
			return v, err
		}
	}
	return v, engine.ErrStale
}

func sameTurn(a, b engine.View) bool {
	old, _ := json.Marshal(a.Data["turn"])
	fresh, _ := json.Marshal(b.Data["turn"])
	return string(old) == string(fresh)
}

func sameDecision(a, b Decision) bool {
	return a.Action == b.Action && string(a.Payload) == string(b.Payload)
}

func generationError(err error) string {
	if errors.Is(err, ErrInvalidAction) {
		return "invalid_action"
	}
	return llm.ErrorCode(err)
}

func submissionError(err error) string {
	switch {
	case errors.Is(err, engine.ErrStale):
		return "stale"
	case errors.Is(err, engine.ErrNotAllowed):
		return "not_allowed"
	case errors.Is(err, engine.ErrEnded):
		return "ended"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "action_rejected"
	}
}

func report(opts Options, r Result) {
	if opts.Stats != nil {
		opts.Stats.record(r)
	}
	if opts.OnResult != nil {
		opts.OnResult(r)
	}
}
