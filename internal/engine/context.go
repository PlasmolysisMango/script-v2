package engine

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"jbs/internal/script"
)

type Ctx struct{ r *Room }

func (c *Ctx) State() *State           { return c.r.st }
func (c *Ctx) Script() *script.Package { return c.r.st.Script }
func (c *Ctx) Step() script.Step       { return c.r.st.Script.Flow[c.r.st.PhaseIdx] }
func (c *Ctx) Seats() []*Seat          { return c.r.st.Seats }
func (c *Ctx) RoomID() string          { return c.r.ID }
func (c *Ctx) PhaseType() string       { return c.Step().Type }
func (c *Ctx) PhaseID() string         { return c.Step().ID }
func (c *Ctx) Act() int                { return c.Step().Int("act", 1) }
func (c *Ctx) Get(key string) any      { return c.r.st.Ext[key] }
func (c *Ctx) Set(key string, v any)   { c.r.st.Ext[key] = v }
func (c *Ctx) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	return c.r.rng.IntN(n)
}
func (c *Ctx) Shuffle(n int, swap func(i, j int)) {
	c.r.rng.Shuffle(n, swap)
}
func (c *Ctx) AllJoined() bool {
	return len(c.Seats()) > 0 && !slices.ContainsFunc(c.Seats(), func(s *Seat) bool { return !s.Joined })
}
func (c *Ctx) Seat(pid string) *Seat {
	for _, s := range c.Seats() {
		if s.ID == pid {
			return s
		}
	}
	return nil
}

func (c *Ctx) Emit(typ, actor string, aud Audience, payload map[string]any) Event {
	c.r.st.StateID++
	e := c.r.appendEvent(Event{Type: typ, Actor: actor, Public: aud.Public, To: aud.To, Payload: payload})
	for _, h := range c.r.reg.hooks {
		if h.OnEvent != nil {
			h.OnEvent(c, e)
		}
	}
	return e
}

func (c *Ctx) Advance()       { c.r.advance = true }
func (c *Ctx) Goto(id string) { c.r.gotoStep = id }

func (c *Ctx) EndGame() {
	if c.State().Ended {
		return
	}
	c.State().Ended = true
	c.Emit("game.end", "", Public(), map[string]any{"room": c.RoomID()})
	for _, h := range c.r.reg.hooks {
		if h.OnGameEnd != nil {
			h.OnGameEnd(c)
		}
	}
}

func (c *Ctx) SetDeadline(d time.Duration) {
	if d <= 0 {
		return
	}
	c.State().Deadline = time.Now().Add(d)
	c.Emit("phase.deadline", "", Public(), map[string]any{"at": c.State().Deadline})
}

func (c *Ctx) Schedule(d time.Duration, fn func(c *Ctx)) {
	r, gen := c.r, c.State().PhaseGen
	time.AfterFunc(d, func() {
		r.post(func() {
			if r.st.Ended || r.st.PhaseGen != gen {
				return
			}
			ctx := r.ctx()
			fn(ctx)
			r.settle(ctx)
		})
	})
}

func (c *Ctx) Allowed(pid string) []ActionSpec {
	if c.State().Ended {
		return nil
	}
	if pid != HostID {
		if s := c.Seat(pid); s == nil || !s.Joined {
			return nil
		}
	}
	var out []ActionSpec
	if pt := c.r.phaseType(); pt.Allowed != nil && pid != HostID {
		out = append(out, pt.Allowed(c, pid)...)
	}
	for _, fn := range c.r.reg.allowed {
		out = append(out, fn(c, pid)...)
	}
	return out
}

func (c *Ctx) Hint(pid string) string {
	var hints []string
	if p := c.r.phaseType(); p.Hint != nil {
		hints = append(hints, p.Hint(c, pid))
	}
	for _, fn := range c.r.reg.hints {
		hints = append(hints, fn(c, pid))
	}
	return strings.Join(hints, "\n")
}

type PhaseView struct {
	ID       string    `json:"id"`
	Type     string    `json:"type"`
	Title    string    `json:"title"`
	Index    int       `json:"index"`
	Act      int       `json:"act"`
	Deadline time.Time `json:"deadline,omitempty"`
}

type CharacterView struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	PublicIntro string           `json:"public_intro"`
	TTS         script.Voice     `json:"tts"`
	Sections    []script.Section `json:"sections,omitempty"`
	Goals       []string         `json:"goals,omitempty"`
	Performance string           `json:"performance,omitempty"`
	Ability     string           `json:"ability,omitempty"`
	CanLie      bool             `json:"can_lie"`
}

type View struct {
	RoomID  string         `json:"room_id"`
	Meta    script.Meta    `json:"meta"`
	StateID int64          `json:"state_id"`
	LastSeq int64          `json:"last_seq"`
	Phase   PhaseView      `json:"phase"`
	Ended   bool           `json:"ended"`
	PID     string         `json:"pid"`
	Seats   []Seat         `json:"seats"`
	Me      *CharacterView `json:"me,omitempty"`
	Allowed []ActionSpec   `json:"allowed_actions"`
	Hint    string         `json:"hint,omitempty"`
	Panels  []Panel        `json:"panels"`
	Data    map[string]any `json:"data"`
}

func (r *Room) view(c *Ctx, pid string) View {
	st := c.State()
	s := c.Step()
	v := View{
		RoomID: r.ID, Meta: st.Script.Meta, StateID: st.StateID, LastSeq: r.LastSeq(),
		Phase: PhaseView{ID: s.ID, Type: s.Type, Title: s.Title, Index: st.PhaseIdx, Act: c.Act(), Deadline: st.Deadline},
		Ended: st.Ended, PID: pid, Allowed: c.Allowed(pid), Hint: c.Hint(pid), Data: map[string]any{},
	}
	for _, seat := range st.Seats {
		v.Seats = append(v.Seats, *seat)
	}
	if ch := st.Script.Character(pid); ch != nil {
		v.Me = &CharacterView{ID: ch.ID, Name: ch.Name, PublicIntro: ch.PublicIntro, TTS: ch.TTS,
			Goals: ch.Goals, Performance: ch.Performance, Ability: ch.Ability, CanLie: ch.CanLie}
		// Sections unlock only when reading begins and remain visible afterward.
		maxAct, _ := st.Ext["reading.act"].(int)
		for _, sec := range ch.Sections {
			if sec.Act <= maxAct {
				v.Me.Sections = append(v.Me.Sections, sec)
			}
		}
	}
	for _, fn := range r.reg.views {
		k, val := fn(c, pid)
		if k != "" && val != nil {
			v.Data[k] = val
		}
	}
	for _, p := range r.reg.panels {
		if p.HostOnly && pid != HostID {
			continue
		}
		if len(p.Phases) == 0 || slices.Contains(p.Phases, s.Type) {
			v.Panels = append(v.Panels, p)
		}
	}
	slices.SortStableFunc(v.Panels, func(a, b Panel) int { return a.Order - b.Order })
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var snapshot View
	if err := json.Unmarshal(b, &snapshot); err != nil {
		panic(err)
	}
	return snapshot
}
