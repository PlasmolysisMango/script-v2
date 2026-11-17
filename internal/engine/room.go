package engine

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"jbs/internal/script"
)

type Store interface {
	Append(roomID string, e Event) error
}

// SnapshotStore optionally persists inspection snapshots, not restart state.
// Snapshot is called synchronously on the room actor.
type SnapshotStore interface {
	Snapshot(roomID string, value any) error
}

// roomSnapshot is private persistence data and must never be used as a public view.
type roomSnapshot struct {
	RoomID          string          `json:"room_id"`
	ScriptID        string          `json:"script_id"`
	PhaseIndex      int             `json:"phase_index"`
	StateID         int64           `json:"state_id"`
	PhaseGeneration int64           `json:"phase_generation"`
	Seats           []Seat          `json:"seats"`
	Ended           bool            `json:"ended"`
	Visited         map[string]bool `json:"visited"`
	Deadline        time.Time       `json:"deadline"`
	Ext             map[string]any  `json:"ext"`
	LastSeq         int64           `json:"last_seq"`
}

type State struct {
	RoomID   string
	Script   *script.Package
	Seats    []*Seat
	PhaseIdx int
	PhaseGen int64
	StateID  int64
	Deadline time.Time
	Ended    bool
	Visited  map[string]bool
	Ext      map[string]any
}

type Msg struct {
	Event *Event `json:"event,omitempty"`
	View  *View  `json:"view,omitempty"`
}

type Sub struct {
	PID  string
	C    chan Msg
	room *Room
}

func (s *Sub) Close() { s.room.post(func() { s.room.dropSub(s) }) }

type Room struct {
	ID        string
	HostToken string
	Created   time.Time

	reg   *Registry
	st    *State
	store Store
	inbox chan func()
	rng   *rand.Rand

	advance   bool
	gotoStep  string
	unflushed []Event
	subs      map[*Sub]struct{}

	mu         sync.Mutex
	events     []Event
	persistErr error
	notify     chan struct{}
	gate       sync.RWMutex
	stop       chan struct{}
	stopOnce   sync.Once
}

func NewRoom(id string, pkg *script.Package, reg *Registry, store Store) (*Room, error) {
	if err := reg.CheckScript(pkg); err != nil {
		return nil, err
	}
	st := &State{RoomID: id, Script: pkg, Visited: map[string]bool{}, Ext: map[string]any{}, PhaseIdx: -1}
	for _, ch := range pkg.Characters {
		st.Seats = append(st.Seats, &Seat{ID: ch.ID, CharName: ch.Name, PublicIntro: ch.PublicIntro})
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	seed := h.Sum64()
	r := &Room{
		ID: id, HostToken: NewToken(), Created: time.Now(),
		reg: reg, st: st, store: store, rng: rand.New(rand.NewPCG(seed, seed>>32)),
		inbox: make(chan func(), 256), subs: map[*Sub]struct{}{}, notify: make(chan struct{}), stop: make(chan struct{}),
	}
	go r.run()
	r.do(func() {
		c := r.ctx()
		c.Emit("room.created", "", Public(), map[string]any{"script": pkg.Meta.ID, "title": pkg.Meta.Title})
		r.enter(c, 0)
		r.settle(c)
	})
	return r, nil
}

func NewToken() string {
	b := make([]byte, 16)
	_, _ = cryptorand.Read(b)
	return hex.EncodeToString(b)
}

func (r *Room) run() {
	defer func() {
		for s := range r.subs {
			r.dropSub(s)
		}
		r.mu.Lock()
		close(r.notify)
		r.mu.Unlock()
	}()
	for {
		select {
		case f := <-r.inbox:
			f()
		case <-r.stop:
			return
		}
	}
}

func (r *Room) do(f func()) {
	r.gate.RLock()
	defer r.gate.RUnlock()
	select {
	case <-r.stop:
		return
	default:
	}
	done := make(chan struct{})
	r.inbox <- func() { defer close(done); f(); r.flush() }
	<-done
}

func (r *Room) post(f func()) {
	r.gate.RLock()
	defer r.gate.RUnlock()
	select {
	case <-r.stop:
		return
	default:
	}
	r.inbox <- func() { f(); r.flush() }
}

func (r *Room) Close() {
	r.stopOnce.Do(func() {
		r.gate.Lock()
		defer r.gate.Unlock()
		close(r.stop)
	})
}

func (r *Room) ctx() *Ctx { return &Ctx{r: r} }

func (r *Room) Record(pid, typ string, payload map[string]any) {
	r.do(func() { r.ctx().Emit(typ, pid, To(pid), payload) })
}

func (r *Room) Script() *script.Package { return r.st.Script }

// Join claims a seat. kind is "human" or "ai". Returns the seat token.
func (r *Room) Join(pid, name, kind string) (string, error) {
	var tok string
	var err error
	r.do(func() {
		s := r.ctx().Seat(pid)
		switch {
		case s == nil:
			err = fmt.Errorf("no such character %q", pid)
		case s.Joined:
			err = fmt.Errorf("seat %q already taken", pid)
		case r.st.Ended:
			err = ErrEnded
		case r.st.PhaseIdx != 0:
			err = fmt.Errorf("cannot join after lobby")
		default:
			s.Joined, s.Name, s.Kind, s.Token = true, name, kind, NewToken()
			tok = s.Token
			c := r.ctx()
			c.Emit("player.join", pid, Public(), map[string]any{"name": name, "kind": kind, "char_name": s.CharName})
			r.settle(c)
		}
	})
	return tok, err
}

func (r *Room) Submit(a Action) error {
	var err error
	r.do(func() {
		c := r.ctx()
		err = r.apply(c, a)
		r.settle(c)
	})
	return err
}

func (r *Room) apply(c *Ctx, a Action) error {
	if r.st.Ended {
		return ErrEnded
	}
	if a.StateID != 0 && a.StateID != r.st.StateID {
		return ErrStale
	}
	specs := c.Allowed(a.Actor)
	if !slices.ContainsFunc(specs, func(s ActionSpec) bool { return s.Type == a.Type }) {
		var names []string
		for _, s := range specs {
			names = append(names, s.Type)
		}
		return fmt.Errorf("%w: %q (allowed: %v)", ErrNotAllowed, a.Type, names)
	}
	for _, h := range r.reg.hooks {
		if h.BeforeAction != nil {
			if err := h.BeforeAction(c, &a); err != nil {
				return err
			}
		}
	}
	h, ok := r.reg.actions[a.Type]
	if !ok {
		return fmt.Errorf("no handler for action %q", a.Type)
	}
	if err := h(c, a); err != nil {
		return err
	}
	for _, h := range r.reg.hooks {
		if h.AfterAction != nil {
			h.AfterAction(c, a)
		}
	}
	return nil
}

func (r *Room) phaseType() PhaseType {
	if r.st.PhaseIdx < 0 || r.st.PhaseIdx >= len(r.st.Script.Flow) {
		return PhaseType{}
	}
	return r.reg.phases[r.st.Script.Flow[r.st.PhaseIdx].Type]
}

func (r *Room) settle(c *Ctx) {
	for range 100 {
		if r.st.Ended {
			return
		}
		pt := r.phaseType()
		if !r.advance && r.gotoStep == "" && (pt.EndIf == nil || !pt.EndIf(c)) {
			return
		}
		next := r.st.PhaseIdx + 1
		cur := r.st.Script.Flow[r.st.PhaseIdx]
		if cur.Next != "" {
			next = r.st.Script.StepIndex(cur.Next)
		}
		if r.gotoStep != "" {
			next = r.st.Script.StepIndex(r.gotoStep)
		}
		r.advance, r.gotoStep = false, ""
		if pt.OnExit != nil {
			pt.OnExit(c)
		}
		for _, h := range r.reg.hooks {
			if h.OnPhaseExit != nil {
				h.OnPhaseExit(c)
			}
		}
		if next < 0 || next >= len(r.st.Script.Flow) {
			c.EndGame()
			return
		}
		r.enter(c, next)
	}
}

func (r *Room) enter(c *Ctx, idx int) {
	st := r.st
	st.PhaseIdx = idx
	st.PhaseGen++
	st.StateID++
	st.Deadline = time.Time{}
	step := st.Script.Flow[idx]
	st.Visited[step.ID] = true
	c.Emit("phase.enter", "", Public(), map[string]any{"id": step.ID, "type": step.Type, "title": step.Title, "index": idx})
	if pt := r.reg.phases[step.Type]; pt.OnEnter != nil {
		pt.OnEnter(c)
	}
	for _, h := range r.reg.hooks {
		if h.OnPhaseEnter != nil && !st.Ended {
			h.OnPhaseEnter(c)
		}
	}
}

func (r *Room) appendEvent(e Event) Event {
	r.mu.Lock()
	e.Seq = int64(len(r.events)) + 1
	e.At = time.Now()
	r.events = append(r.events, e)
	r.mu.Unlock()
	if r.store != nil {
		if err := r.store.Append(r.ID, e); err != nil {
			r.recordPersistError(fmt.Errorf("append event %d: %w", e.Seq, err))
		}
	}
	r.unflushed = append(r.unflushed, e)
	return e
}

func (r *Room) flush() {
	evs := r.unflushed
	r.unflushed = nil
	if len(evs) == 0 {
		return
	}
	if store, ok := r.store.(SnapshotStore); ok {
		st := r.st
		seats := make([]Seat, 0, len(st.Seats))
		for _, seat := range st.Seats {
			s := *seat
			s.Token = ""
			seats = append(seats, s)
		}
		snapshot := roomSnapshot{
			RoomID: r.ID, ScriptID: st.Script.Meta.ID, PhaseIndex: st.PhaseIdx,
			StateID: st.StateID, PhaseGeneration: st.PhaseGen, Seats: seats,
			Ended: st.Ended, Visited: st.Visited, Deadline: st.Deadline,
			Ext: st.Ext, LastSeq: evs[len(evs)-1].Seq,
		}
		if err := store.Snapshot(r.ID, snapshot); err != nil {
			r.recordPersistError(fmt.Errorf("snapshot: %w", err))
		}
	}
	c := r.ctx()
	for s := range r.subs {
		ok := true
		for i := range evs {
			if evs[i].VisibleTo(s.PID) {
				ok = ok && r.send(s, Msg{Event: &evs[i]})
			}
		}
		if ok {
			v := r.view(c, s.PID)
			ok = r.send(s, Msg{View: &v})
		}
		if !ok {
			r.dropSub(s)
		}
	}
	r.mu.Lock()
	close(r.notify)
	r.notify = make(chan struct{})
	r.mu.Unlock()
}

func (r *Room) send(s *Sub, m Msg) bool {
	select {
	case s.C <- m:
		return true
	default:
		return false
	}
}

func (r *Room) dropSub(s *Sub) {
	if _, ok := r.subs[s]; ok {
		delete(r.subs, s)
		close(s.C)
	}
}

// Subscribe streams events visible to pid followed by a fresh view after each batch.
func (r *Room) Subscribe(pid string, buf int) *Sub {
	s := &Sub{PID: pid, C: make(chan Msg, buf), room: r}
	r.do(func() {
		r.subs[s] = struct{}{}
		v := r.view(r.ctx(), pid)
		s.C <- Msg{View: &v}
	})
	return s
}

func (r *Room) View(pid string) View {
	var v View
	r.do(func() { v = r.view(r.ctx(), pid) })
	return v
}

// Log returns events visible to pid with seq > since.
func (r *Room) Log(pid string, since int64) []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Event
	for i := max(since, 0); i < int64(len(r.events)); i++ {
		if r.events[i].VisibleTo(pid) {
			out = append(out, r.events[i])
		}
	}
	return out
}

func (r *Room) LastSeq() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return int64(len(r.events))
}

func (r *Room) recordPersistError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.persistErr = errors.Join(r.persistErr, err)
}

// PersistError returns accumulated persistence failures, including after Close.
// Failures are not included in public events or views.
func (r *Room) PersistError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.persistErr
}

// Wait blocks until an event with seq > since exists or ctx is done.
func (r *Room) Wait(ctx context.Context, since int64) {
	r.mu.Lock()
	if int64(len(r.events)) > since {
		r.mu.Unlock()
		return
	}
	ch := r.notify
	r.mu.Unlock()
	select {
	case <-ch:
	case <-ctx.Done():
	}
}

// Token resolves a token to a player id (or HostID).
func (r *Room) Token(tok string) (string, bool) {
	if tok == "" {
		return "", false
	}
	if tok == r.HostToken {
		return HostID, true
	}
	var pid string
	r.do(func() {
		for _, s := range r.st.Seats {
			if s.Token == tok {
				pid = s.ID
			}
		}
	})
	return pid, pid != ""
}

type RoomInfo struct {
	ID      string    `json:"id"`
	Script  string    `json:"script"`
	Title   string    `json:"title"`
	Phase   string    `json:"phase"`
	Ended   bool      `json:"ended"`
	Seats   []Seat    `json:"seats"`
	Created time.Time `json:"created"`
}

func (r *Room) Info() RoomInfo {
	var ri RoomInfo
	r.do(func() {
		ri = RoomInfo{ID: r.ID, Script: r.st.Script.Meta.ID, Title: r.st.Script.Meta.Title, Ended: r.st.Ended, Created: r.Created}
		if r.st.PhaseIdx >= 0 {
			ri.Phase = r.st.Script.Flow[r.st.PhaseIdx].Title
		}
		for _, s := range r.st.Seats {
			ri.Seats = append(ri.Seats, *s)
		}
	})
	return ri
}
