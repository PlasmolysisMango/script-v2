package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"jbs/internal/script"
)

const HostID = "host"

var (
	ErrEnded      = errors.New("game has ended")
	ErrStale      = errors.New("stale state_id: the game state changed, refresh and retry")
	ErrNotAllowed = errors.New("action not allowed now")
)

type Event struct {
	Seq     int64          `json:"seq"`
	At      time.Time      `json:"at"`
	Type    string         `json:"type"`
	Actor   string         `json:"actor,omitempty"`
	Public  bool           `json:"public"`
	To      []string       `json:"to,omitempty"`
	Payload map[string]any `json:"payload,omitempty"`
}

// VisibleTo reports whether pid may see the event. pid "*" sees everything.
func (e *Event) VisibleTo(pid string) bool {
	return e.Public || pid == "*" || slices.Contains(e.To, pid)
}

type Audience struct {
	Public bool
	To     []string
}

func Public() Audience                 { return Audience{Public: true} }
func To(pids ...string) Audience       { return Audience{To: pids} }
func (a Audience) String() string      { return fmt.Sprintf("public=%v to=%v", a.Public, a.To) }
func (e *Event) Str(key string) string { s, _ := e.Payload[key].(string); return s }

type Seat struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	CharName    string `json:"char_name"`
	PublicIntro string `json:"public_intro"`
	Kind        string `json:"kind"` // human | ai
	Joined      bool   `json:"joined"`
	Ready       bool   `json:"ready"`
	Token       string `json:"-"`
}

type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type ActionSpec struct {
	Type     string            `json:"type"`
	Desc     string            `json:"desc"`
	Params   map[string]string `json:"params,omitempty"`
	Options  []Option          `json:"options,omitempty"`
	Optional bool              `json:"optional,omitempty"`
}

type Action struct {
	Type    string          `json:"type"`
	Actor   string          `json:"actor,omitempty"`
	StateID int64           `json:"state_id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func (a Action) Decode(v any) error {
	if len(a.Payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(a.Payload, v); err != nil {
		return fmt.Errorf("bad payload for %s: %w", a.Type, err)
	}
	return nil
}

func NewAction(typ string, payload any) Action {
	a := Action{Type: typ}
	if payload != nil {
		a.Payload, _ = json.Marshal(payload)
	}
	return a
}

type ActionHandler func(c *Ctx, a Action) error

type PhaseType struct {
	OnEnter func(c *Ctx)
	OnExit  func(c *Ctx)
	EndIf   func(c *Ctx) bool
	Allowed func(c *Ctx, pid string) []ActionSpec
	Hint    func(c *Ctx, pid string) string
}

type Hooks struct {
	BeforeAction func(c *Ctx, a *Action) error
	AfterAction  func(c *Ctx, a Action)
	OnEvent      func(c *Ctx, e Event)
	OnPhaseEnter func(c *Ctx)
	OnPhaseExit  func(c *Ctx)
	OnGameEnd    func(c *Ctx)
}

type Panel struct {
	ID      string   `json:"id"`
	Plugin  string   `json:"plugin"`
	Kind    string   `json:"kind"`
	Title   string   `json:"title"`
	Phases  []string `json:"phases,omitempty"` // phase types; empty = always
	DataKey string   `json:"data_key,omitempty"`
	Order   int      `json:"order"`
}

type ViewFn func(c *Ctx, pid string) (key string, v any)

type Plugin interface {
	Name() string
	Init(r *Registry) error
}

type Registry struct {
	plugins []Plugin
	actions map[string]ActionHandler
	phases  map[string]PhaseType
	hooks   []Hooks
	views   []ViewFn
	panels  []Panel
	allowed []func(c *Ctx, pid string) []ActionSpec
	hints   []func(c *Ctx, pid string) string
}

func NewRegistry(plugins ...Plugin) (*Registry, error) {
	r := &Registry{actions: map[string]ActionHandler{}, phases: map[string]PhaseType{}}
	r.PhaseType("end", PhaseType{OnEnter: func(c *Ctx) { c.EndGame() }})
	for _, p := range plugins {
		if err := p.Init(r); err != nil {
			return nil, fmt.Errorf("plugin %s: %w", p.Name(), err)
		}
		r.plugins = append(r.plugins, p)
	}
	return r, nil
}

func (r *Registry) Action(typ string, h ActionHandler)      { r.actions[typ] = h }
func (r *Registry) PhaseType(typ string, p PhaseType)       { r.phases[typ] = p }
func (r *Registry) Hook(h Hooks)                            { r.hooks = append(r.hooks, h) }
func (r *Registry) View(fn ViewFn)                          { r.views = append(r.views, fn) }
func (r *Registry) Panel(p Panel)                           { r.panels = append(r.panels, p) }
func (r *Registry) Hint(fn func(c *Ctx, pid string) string) { r.hints = append(r.hints, fn) }

func (r *Registry) PromptFragment(phaseType string, fn func(c *Ctx, pid string) string) {
	r.Hint(func(c *Ctx, pid string) string {
		if phaseType == "" || c.PhaseType() == phaseType {
			return fn(c, pid)
		}
		return ""
	})
}

// Allowed registers a phase-independent contributor of allowed actions.
func (r *Registry) Allowed(fn func(c *Ctx, pid string) []ActionSpec) {
	r.allowed = append(r.allowed, fn)
}

func (r *Registry) Plugins() []string {
	var out []string
	for _, p := range r.plugins {
		out = append(out, p.Name())
	}
	return out
}

// CheckScript verifies every flow step type is provided by a registered plugin.
func (r *Registry) CheckScript(p *script.Package) error {
	for _, s := range p.Flow {
		if _, ok := r.phases[s.Type]; !ok {
			return fmt.Errorf("flow step %s: phase type %q not provided by any plugin", s.ID, s.Type)
		}
	}
	return nil
}
