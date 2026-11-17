package clues

import (
	"fmt"

	"jbs/internal/engine"
	"jbs/internal/plugins/ready"
	"jbs/internal/script"
)

type Plugin struct{}

type state struct {
	Owners map[string]string
	Public map[string]bool
	AP     map[string]int
}

type Location struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Remaining int    `json:"remaining"`
}

type View struct {
	Own       []script.Clue `json:"own"`
	Public    []script.Clue `json:"public"`
	Locations []Location    `json:"locations"`
	AP        int           `json:"ap"`
}

func (Plugin) Name() string { return "clues" }
func (Plugin) Init(r *engine.Registry) error {
	r.PhaseType("search", engine.PhaseType{
		OnEnter: func(c *engine.Ctx) {
			ready.Reset(c)
			s := get(c)
			s.AP = map[string]int{}
			for _, seat := range c.Seats() {
				s.AP[seat.ID] = max(0, c.Step().Int("action_points", 3))
			}
			exhausted(c, s)
		}, EndIf: ready.AllReady, Allowed: allowed,
		Hint: func(c *engine.Ctx, pid string) string {
			if seat := c.Seat(pid); seat != nil && !seat.Ready {
				return "Search a location, publish your clues, or mark ready even with action points left."
			}
			return "Waiting for the other players; you may still publish your clues."
		},
	})
	r.Action("search", search)
	r.Action("publish", publish)
	r.Allowed(func(c *engine.Ctx, pid string) []engine.ActionSpec {
		if pid == engine.HostID {
			return nil
		}
		s := get(c)
		var options []engine.Option
		for _, clue := range c.Script().Clues {
			if s.Owners[clue.ID] == pid && !s.Public[clue.ID] {
				options = append(options, engine.Option{Value: clue.ID, Label: clue.Title})
			}
		}
		if len(options) == 0 {
			return nil
		}
		return []engine.ActionSpec{{Type: "publish", Desc: "Publish one of your clues", Params: map[string]string{"clue": "Owned clue ID"}, Options: options, Optional: true}}
	})
	r.Hook(engine.Hooks{
		OnPhaseEnter: func(c *engine.Ctx) {
			if c.PhaseType() != "reading" {
				return
			}
			s := get(c)
			// Iterate to unlock chains of public clues independent of script order.
			for {
				changed := false
				for _, clue := range c.Script().Clues {
					if clue.Visibility == "public" && !s.Public[clue.ID] && eligible(c, s, clue) {
						s.Public[clue.ID] = true
						c.Emit("clue.public", "", engine.Public(), payload(clue))
						changed = true
					}
				}
				if !changed {
					break
				}
			}
		},
		OnEvent: func(c *engine.Ctx, e engine.Event) {
			// room.created is emitted before there is a current step.
			if e.Type == "player.join" && c.PhaseType() == "search" {
				exhausted(c, get(c))
			}
		},
	})
	r.View(func(c *engine.Ctx, pid string) (string, any) {
		s := get(c)
		v := View{Own: []script.Clue{}, Public: []script.Clue{}, Locations: locations(c, s), AP: s.AP[pid]}
		for _, clue := range c.Script().Clues {
			if owner, ok := s.Owners[clue.ID]; ok && owner == pid {
				v.Own = append(v.Own, clue)
			}
			if s.Public[clue.ID] {
				v.Public = append(v.Public, clue)
			}
		}
		return "clues", v
	})
	r.Panel(engine.Panel{ID: "clues", Plugin: "clues", Kind: "clues", Title: "Clues", DataKey: "clues", Order: 20})
	return nil
}

func get(c *engine.Ctx) *state {
	s, _ := c.Get("clues.state").(*state)
	if s == nil {
		s = &state{Owners: map[string]string{}, Public: map[string]bool{}, AP: map[string]int{}}
		c.Set("clues.state", s)
	}
	return s
}

func act(c *engine.Ctx) int {
	if c.PhaseType() == "reading" || c.PhaseType() == "search" {
		return c.Act()
	}
	n, _ := c.Get("reading.act").(int)
	return n
}

func eligible(c *engine.Ctx, s *state, clue script.Clue) bool {
	loc := c.Script().Location(clue.Location)
	if clue.Act > act(c) || loc == nil || loc.Act > act(c) {
		return false
	}
	if clue.Unlock != nil {
		if p := clue.Unlock.RequiresPhase; p != "" && !c.State().Visited[p] {
			return false
		}
		for _, id := range clue.Unlock.RequiresClues {
			if _, found := s.Owners[id]; !found && !s.Public[id] {
				return false
			}
		}
	}
	return true
}

func available(c *engine.Ctx, s *state, clue script.Clue) bool {
	_, found := s.Owners[clue.ID]
	return !found && !s.Public[clue.ID] && clue.Visibility != "public" && eligible(c, s, clue)
}

func locations(c *engine.Ctx, s *state) []Location {
	out := []Location{}
	for _, loc := range c.Script().Locations {
		if loc.Act > act(c) {
			continue
		}
		v := Location{ID: loc.ID, Name: loc.Name}
		for _, clue := range c.Script().Clues {
			if clue.Location == loc.ID && available(c, s, clue) {
				v.Remaining++
			}
		}
		out = append(out, v)
	}
	return out
}

func allowed(c *engine.Ctx, pid string) []engine.ActionSpec {
	out := ready.Allowed(c, pid)
	s := get(c)
	seat := c.Seat(pid)
	if seat == nil || seat.Ready || s.AP[pid] <= 0 {
		return out
	}
	var options []engine.Option
	for _, loc := range locations(c, s) {
		if loc.Remaining > 0 {
			options = append(options, engine.Option{Value: loc.ID, Label: loc.Name})
		}
	}
	if len(options) > 0 {
		out = append(out, engine.ActionSpec{Type: "search", Desc: "Search a location", Params: map[string]string{"location": "Location ID"}, Options: options})
	}
	return out
}

func search(c *engine.Ctx, a engine.Action) error {
	s := get(c)
	seat := c.Seat(a.Actor)
	if c.PhaseType() != "search" || seat == nil || !seat.Joined || seat.Ready || s.AP[a.Actor] <= 0 {
		return engine.ErrNotAllowed
	}
	var p struct {
		Location string `json:"location"`
	}
	if err := a.Decode(&p); err != nil {
		return err
	}
	for _, clue := range c.Script().Clues {
		if clue.Location == p.Location && available(c, s, clue) {
			s.Owners[clue.ID] = a.Actor
			s.AP[a.Actor]--
			data := payload(clue)
			data["ap"] = s.AP[a.Actor]
			c.Emit("clue.found", a.Actor, engine.To(a.Actor), data)
			exhausted(c, s)
			return nil
		}
	}
	return fmt.Errorf("no available clue at location %q", p.Location)
}

func publish(c *engine.Ctx, a engine.Action) error {
	var p struct {
		Clue string `json:"clue"`
	}
	if err := a.Decode(&p); err != nil {
		return err
	}
	s := get(c)
	clue := c.Script().Clue(p.Clue)
	owner, found := s.Owners[p.Clue]
	if clue == nil || !found || owner != a.Actor || s.Public[p.Clue] {
		return fmt.Errorf("clue must be owned by you and not already public")
	}
	s.Public[p.Clue] = true
	c.Emit("clue.published", a.Actor, engine.Public(), payload(*clue))
	return nil
}

func exhausted(c *engine.Ctx, s *state) {
	remaining := 0
	for _, loc := range locations(c, s) {
		remaining += loc.Remaining
	}
	for _, seat := range c.Seats() {
		if s.AP[seat.ID] <= 0 || remaining == 0 {
			ready.Mark(c, seat.ID)
		}
	}
}

func payload(clue script.Clue) map[string]any {
	return map[string]any{"id": clue.ID, "location": clue.Location, "title": clue.Title, "body": clue.Body, "act": clue.Act}
}
