package clues

import (
	"fmt"
	"slices"

	"jbs/internal/engine"
	"jbs/internal/plugins/points"
	"jbs/internal/plugins/ready"
	"jbs/internal/script"
)

type Plugin struct{}

type state struct {
	Owners map[string]string
	Public map[string]bool
	Items  map[string][]string

	Mode         string
	Pool         []string
	Quota        map[string]int
	Drawn        map[string]bool
	Kinds        map[string]string
	Order        []string
	Current      string
	Only         map[string]bool
	RevealOnFind bool
	Leftover     string
	LeftPublic   bool
	Done         bool
}

type Location struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Remaining int    `json:"remaining"`
}

type PoolItem struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Kind   string `json:"kind,omitempty"`
	Number int    `json:"number,omitempty"`
}

type View struct {
	Mode      string         `json:"mode"`
	Own       []script.Clue  `json:"own"`
	Public    []script.Clue  `json:"public"`
	Locations []Location     `json:"locations"`
	Pool      []PoolItem     `json:"pool,omitempty"`
	AP        int            `json:"ap"`
	Quota     int            `json:"quota"`
	PicksLeft int            `json:"picks_left"`
	Kinds     map[string]int `json:"kinds,omitempty"`
	Current   string         `json:"current,omitempty"`
	Order     []string       `json:"order,omitempty"`
	Items     []string       `json:"items,omitempty"`
	Done      bool           `json:"done"`
}

func (Plugin) Name() string { return "clues" }
func (Plugin) Init(r *engine.Registry) error {
	r.PhaseType("search", engine.PhaseType{
		OnEnter: enter, OnExit: exit, EndIf: ready.AllReady, Allowed: allowed,
		Hint: func(c *engine.Ctx, pid string) string {
			if pid == engine.HostID {
				return ""
			}
			s := get(c)
			if seat := c.Seat(pid); s.Current != "" && s.Current != pid && seat != nil && !seat.Ready {
				return "Waiting for the current investigator; you may publish your clues."
			}
			if s.Quota[pid] > 0 {
				return "Pick a clue or search the pool. Reveal findings or mark ready when finished."
			}
			return "You have finished investigating. Mark ready."
		},
	})
	r.Action("search", search)
	r.Action("publish", publish)
	r.Action("host.grant_clue", grant)
	r.Action("host.show_clues", show)
	r.Action("host.extra_pick", extraPick)
	r.Action("host.order", func(c *engine.Ctx, a engine.Action) error {
		if a.Actor != engine.HostID {
			return engine.ErrNotAllowed
		}
		var p struct {
			Order []string `json:"order"`
		}
		if err := a.Decode(&p); err != nil {
			return err
		}
		s := get(c)
		s.Order = nil
		for _, pid := range p.Order {
			if c.Seat(pid) != nil {
				s.Order = append(s.Order, pid)
			}
		}
		s.Current = nextTurn(c, s, "")
		return nil
	})
	r.Allowed(func(c *engine.Ctx, pid string) []engine.ActionSpec {
		if pid == engine.HostID {
			return hostActions(c)
		}
		s := get(c)
		var out []engine.ActionSpec
		for _, clue := range c.Script().Clues {
			if s.Owners[clue.ID] == pid && !s.Public[clue.ID] {
				out = append(out, engine.ActionSpec{Type: "publish", Desc: "Publish one of your clues",
					Params: map[string]string{"clue": "Owned clue ID"}, Options: []engine.Option{{Value: clue.ID, Label: clue.Title}}, Optional: true})
				break
			}
		}
		return out
	})
	r.Hook(engine.Hooks{
		OnPhaseEnter: func(c *engine.Ctx) {
			s := get(c)
			switch c.PhaseType() {
			case "reading", "opening":
				DistributePrivate(c, stringList(c.Step().Params["private_clues"]))
				if c.PhaseType() == "reading" && firstReading(c) {
					DistributePrivate(c, initialOwnerClues(c))
				}
			}
			if c.PhaseType() != "reading" {
				return
			}
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
			if e.Type == "points.show_clues" {
				target := e.Str("target")
				if target == "" {
					target = e.Str("actor")
				}
				limit := 1
				if n, ok := e.Payload["count"].(int); ok && n > 0 {
					limit = n
				}
				public := false
				if b, ok := e.Payload["public"].(bool); ok {
					public = b
				}
				revealOwn(c, target, limit, public)
			}
		},
	})
	r.View(func(c *engine.Ctx, pid string) (string, any) {
		s := get(c)
		v := View{Mode: s.Mode, Own: []script.Clue{}, Public: []script.Clue{}, Locations: locations(c, s),
			Quota: s.Quota[pid], PicksLeft: s.Quota[pid], AP: s.Quota[pid], Current: s.Current, Order: s.Order,
			Done: s.Done, Items: append([]string{}, s.Items[pid]...)}
		v.Kinds = map[string]int{}
		for _, clue := range c.Script().Clues {
			if owner, ok := s.Owners[clue.ID]; ok && owner == pid {
				v.Own = append(v.Own, clue)
			}
			if s.Public[clue.ID] {
				v.Public = append(v.Public, clue)
			}
		}
		if s.Mode == "pool" {
			for _, id := range s.Pool {
				if s.Drawn[id] {
					continue
				}
				clue := c.Script().Clue(id)
				if clue == nil || s.Owners[id] != "" {
					continue
				}
				v.Pool = append(v.Pool, PoolItem{ID: clue.ID, Title: clue.Title, Kind: clue.Kind, Number: clue.Number})
				if clue.Kind != "" {
					v.Kinds[clue.Kind]++
				}
			}
		}
		if pid == engine.HostID {
			v.Pool = nil
			for _, id := range s.Pool {
				if !s.Drawn[id] {
					if clue := c.Script().Clue(id); clue != nil {
						v.Pool = append(v.Pool, PoolItem{ID: clue.ID, Title: clue.Title, Kind: clue.Kind, Number: clue.Number})
					}
				}
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
		s = &state{Owners: map[string]string{}, Public: map[string]bool{}, Items: map[string][]string{},
			Quota: map[string]int{}, Drawn: map[string]bool{}, Kinds: map[string]string{}, Only: map[string]bool{}}
		c.Set("clues.state", s)
	}
	return s
}

func enter(c *engine.Ctx) {
	ready.Reset(c)
	s := get(c)
	s.Mode, s.Pool, s.Current, s.Done, s.Leftover = "", nil, "", false, ""
	s.Quota, s.Drawn, s.Kinds, s.Order, s.Only = map[string]int{}, map[string]bool{}, map[string]string{}, nil, map[string]bool{}
	s.RevealOnFind = c.Step().Bool("reveal_on_find", c.Step().Bool("revealOnFind", false))
	s.Leftover = c.Step().Str("leftover_role_id", c.Step().Str("leftoverRoleId", ""))
	s.LeftPublic = c.Step().Bool("leftover_reveal", c.Step().Bool("leftoverReveal", false))

	DistributePrivate(c, stringList(c.Step().Params["private_clues"]))
	pool := stringList(c.Step().Params["clue_pool"])
	if len(pool) == 0 {
		pool = stringList(c.Step().Params["cluePool"])
	}
	if len(pool) == 0 {
		s.Mode = "location"
		for _, seat := range c.Seats() {
			s.Quota[seat.ID] = max(0, c.Step().Int("action_points", 3))
		}
		exhausted(c, s)
		return
	}
	s.Mode = "pool"
	for _, id := range pool {
		clue := c.Script().Clue(id)
		if clue == nil || s.Owners[id] != "" || s.Public[id] { // owner clues never enter a public pool
			continue
		}
		s.Pool = append(s.Pool, id)
		s.Kinds[id] = clue.Kind
	}
	c.Shuffle(len(s.Pool), func(i, j int) { s.Pool[i], s.Pool[j] = s.Pool[j], s.Pool[i] })
	base := c.Step().Int("per_player_pick", c.Step().Int("perPlayerPick", 0))
	if base <= 0 {
		base = 1
	}
	for _, id := range stringList(c.Step().Params["only_role_ids"]) {
		s.Only[id] = true
	}
	if len(s.Only) == 0 {
		for _, id := range stringList(c.Step().Params["onlyRoleIds"]) {
			s.Only[id] = true
		}
	}
	for _, seat := range c.Seats() {
		if !seat.Joined {
			continue
		}
		if len(s.Only) > 0 && !s.Only[seat.ID] {
			ready.Mark(c, seat.ID)
			continue
		}
		s.Quota[seat.ID] = base + points.ExtraDraws(c, seat.ID)
	}
	s.Order = buildOrder(c, s)
	s.Current = nextTurn(c, s, "")
	c.Emit("search.order", "", engine.Public(), map[string]any{"order": s.Order, "mode": "pool", "quota": s.Quota})
	if s.Current == "" {
		finishAll(c, s)
	}
}

func exit(c *engine.Ctx) {
	s := get(c)
	if s == nil || s.Done {
		return
	}
	if s.Leftover == "" {
		return
	}
	for _, id := range s.Pool {
		if s.Drawn[id] {
			continue
		}
		clue := c.Script().Clue(id)
		if clue == nil {
			continue
		}
		s.Drawn[id] = true
		s.Owners[id] = s.Leftover
		c.Emit("clue.found", s.Leftover, engine.To(s.Leftover), payload(*clue))
		if s.LeftPublic {
			s.Public[id] = true
			c.Emit("clue.published", "", engine.Public(), payload(*clue))
		}
	}
}

func buildOrder(c *engine.Ctx, s *state) []string {
	explicit := stringList(c.Step().Params["order_role_ids"])
	if len(explicit) == 0 {
		explicit = stringList(c.Step().Params["orderRoleIds"])
	}
	mode := c.Step().Str("order", "round_robin")
	if mode == "favor" {
		out := []string{}
		for _, pid := range points.Rank(c) {
			if len(s.Only) == 0 || s.Only[pid] {
				if c.Seat(pid) != nil && s.Quota[pid] > 0 {
					out = append(out, pid)
				}
			}
		}
		return out
	}
	out := []string{}
	seen := map[string]bool{}
	add := func(pid string) {
		if !seen[pid] && c.Seat(pid) != nil && c.Seat(pid).Joined && (len(s.Only) == 0 || s.Only[pid]) {
			seen[pid] = true
			out = append(out, pid)
		}
	}
	for _, pid := range explicit {
		add(pid)
	}
	if len(out) == 0 || mode == "round_robin" {
		for _, seat := range c.Seats() {
			add(seat.ID)
		}
	}
	if mode == "random" {
		c.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	}
	return out
}

func nextTurn(c *engine.Ctx, s *state, after string) string {
	if len(s.Order) == 0 {
		return ""
	}
	start := 0
	if after != "" {
		if i := slices.Index(s.Order, after); i >= 0 {
			start = i + 1
		}
	}
	for i := 0; i < len(s.Order); i++ {
		pid := s.Order[(start+i)%len(s.Order)]
		if s.Quota[pid] > 0 && (s.Current == "" || s.Current != pid || i > 0) {
			return pid
		}
	}
	return ""
}

func advance(c *engine.Ctx, s *state) {
	if s.Current == "" {
		return
	}
	next := nextTurn(c, s, s.Current)
	if next == "" {
		s.Current = ""
		finishAll(c, s)
		return
	}
	s.Current = next
	c.Emit("search.turn", "", engine.Public(), map[string]any{"pid": next})
}

func finishAll(c *engine.Ctx, s *state) {
	s.Done = true
	for _, seat := range c.Seats() {
		ready.Mark(c, seat.ID)
	}
}

func allowed(c *engine.Ctx, pid string) []engine.ActionSpec {
	out := ready.Allowed(c, pid)
	s := get(c)
	seat := c.Seat(pid)
	if seat == nil || !seat.Joined || seat.Ready || s.Done || s.Quota[pid] <= 0 {
		return out
	}
	if s.Mode == "location" {
		var options []engine.Option
		for _, loc := range locations(c, s) {
			if loc.Remaining > 0 {
				options = append(options, engine.Option{Value: loc.ID, Label: loc.Name})
			}
		}
		if len(options) > 0 {
			out = append(out, engine.ActionSpec{Type: "search", Desc: "Search a location",
				Params: map[string]string{"location": "Location ID"}, Options: options})
		}
		return out
	}
	if s.Current != "" && s.Current != pid {
		return out
	}
	spec := engine.ActionSpec{Type: "search", Desc: "Search the clue pool"}
	if stringsMode(c) == "pick" {
		var options []engine.Option
		for _, id := range s.Pool {
			if s.Drawn[id] || s.Owners[id] != "" {
				continue
			}
			if clue := c.Script().Clue(id); clue != nil {
				options = append(options, engine.Option{Value: id, Label: clue.Title})
			}
		}
		if len(options) == 0 {
			return out
		}
		spec.Params = map[string]string{"clue": "Clue ID"}
		spec.Options = options
		spec.Desc = "Choose a clue to investigate"
	} else if len(s.Kinds) > 0 {
		var options []engine.Option
		seen := map[string]bool{}
		for _, kind := range s.Kinds {
			if kind == "" || seen[kind] {
				continue
			}
			seen[kind] = true
			options = append(options, engine.Option{Value: kind, Label: kind})
		}
		if len(options) > 0 {
			spec.Fields = []engine.Field{{Key: "kind", Label: "Clue category", Kind: "select", Options: options,
				Placeholder: "Optional; leaves the draw random"}}
			spec.Desc = "Search the clue pool (optionally by category)"
		}
	}
	out = append(out, spec)
	return out
}

func stringsMode(c *engine.Ctx) string {
	if m := c.Step().Str("search_mode", ""); m != "" {
		return m
	}
	return c.Step().Str("searchMode", "random")
}

func search(c *engine.Ctx, a engine.Action) error {
	s := get(c)
	seat := c.Seat(a.Actor)
	if c.PhaseType() != "search" || seat == nil || !seat.Joined || seat.Ready || s.Quota[a.Actor] <= 0 || s.Done {
		return engine.ErrNotAllowed
	}
	var p struct {
		Clue     string `json:"clue"`
		Kind     string `json:"kind"`
		Location string `json:"location"`
	}
	if err := a.Decode(&p); err != nil {
		return err
	}
	if s.Mode == "location" {
		return searchLocation(c, s, a.Actor, p.Location)
	}
	if s.Current != "" && s.Current != a.Actor {
		return fmt.Errorf("wait for your turn to investigate")
	}
	var clue *script.Clue
	switch {
	case p.Clue != "":
		if !slices.Contains(s.Pool, p.Clue) || s.Drawn[p.Clue] || s.Owners[p.Clue] != "" {
			return fmt.Errorf("clue %q is unavailable", p.Clue)
		}
		clue = c.Script().Clue(p.Clue)
	case p.Kind != "":
		var candidates []string
		for _, id := range s.Pool {
			if s.Drawn[id] || s.Owners[id] != "" {
				continue
			}
			if cl := c.Script().Clue(id); cl != nil && (p.Kind == "" || cl.Kind == p.Kind) {
				candidates = append(candidates, id)
			}
		}
		if len(candidates) == 0 {
			return fmt.Errorf("no clues of kind %q remain", p.Kind)
		}
		clue = c.Script().Clue(candidates[c.Intn(len(candidates))])
	default:
		var candidates []string
		for _, id := range s.Pool {
			if !s.Drawn[id] && s.Owners[id] == "" {
				candidates = append(candidates, id)
			}
		}
		if len(candidates) == 0 {
			return fmt.Errorf("the clue pool is empty")
		}
		clue = c.Script().Clue(candidates[c.Intn(len(candidates))])
	}
	if clue == nil {
		return fmt.Errorf("unknown clue")
	}
	s.Drawn[clue.ID] = true
	s.Owners[clue.ID] = a.Actor
	s.Quota[a.Actor]--
	if cost := c.Step().Int("draw_cost", 0); cost > 0 {
		points.Adjust(c, a.Actor, -cost)
	}
	data := payload(*clue)
	data["picks_left"] = s.Quota[a.Actor]
	c.Emit("clue.found", a.Actor, engine.To(a.Actor), data)
	if s.RevealOnFind || clue.MustReveal || clue.Visibility == "public" {
		s.Public[clue.ID] = true
		c.Emit("clue.published", a.Actor, engine.Public(), payload(*clue))
	}
	if clue.GrantsItem != "" {
		s.Items[a.Actor] = append(s.Items[a.Actor], clue.GrantsItem)
		c.Emit("item.granted", a.Actor, engine.To(a.Actor), map[string]any{"item": clue.GrantsItem})
	}
	for _, id := range clue.GrantsClue {
		if granted := c.Script().Clue(id); granted != nil && s.Owners[id] == "" && !s.Public[id] {
			s.Owners[id] = a.Actor
			c.Emit("clue.found", a.Actor, engine.To(a.Actor), payload(*granted))
		}
	}
	if clue.ExtraDraw {
		s.Quota[a.Actor]++
	}
	if s.Quota[a.Actor] <= 0 {
		advance(c, s)
	}
	exhausted(c, s)
	return nil
}

func searchLocation(c *engine.Ctx, s *state, pid, location string) error {
	for _, clue := range c.Script().Clues {
		if clue.Location == location && available(c, s, clue) {
			s.Owners[clue.ID] = pid
			s.Quota[pid]--
			data := payload(clue)
			data["ap"] = s.Quota[pid]
			c.Emit("clue.found", pid, engine.To(pid), data)
			if clue.MustReveal || clue.Visibility == "public" {
				s.Public[clue.ID] = true
				c.Emit("clue.published", pid, engine.Public(), payload(clue))
			}
			exhausted(c, s)
			return nil
		}
	}
	return fmt.Errorf("no available clue at location %q", location)
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

func grant(c *engine.Ctx, a engine.Action) error {
	if a.Actor != engine.HostID {
		return engine.ErrNotAllowed
	}
	var p struct {
		Target string `json:"target"`
		Clue   string `json:"clue"`
		Public bool   `json:"public"`
	}
	if err := a.Decode(&p); err != nil {
		return err
	}
	clue := c.Script().Clue(p.Clue)
	if clue == nil || c.Seat(p.Target) == nil {
		return fmt.Errorf("unknown target or clue")
	}
	s := get(c)
	s.Owners[clue.ID] = p.Target
	if p.Public {
		s.Public[clue.ID] = true
	}
	c.Emit("clue.found", p.Target, engine.To(p.Target), payload(*clue))
	if p.Public {
		c.Emit("clue.published", "", engine.Public(), payload(*clue))
	}
	return nil
}

func show(c *engine.Ctx, a engine.Action) error {
	if a.Actor != engine.HostID {
		return engine.ErrNotAllowed
	}
	var p struct {
		Target string `json:"target"`
		Public bool   `json:"public"`
	}
	if err := a.Decode(&p); err != nil {
		return err
	}
	revealOwn(c, p.Target, 1000, p.Public)
	return nil
}

func extraPick(c *engine.Ctx, a engine.Action) error {
	if a.Actor != engine.HostID {
		return engine.ErrNotAllowed
	}
	var p struct {
		Target string `json:"target"`
		Count  int    `json:"count"`
	}
	if err := a.Decode(&p); err != nil {
		return err
	}
	s := get(c)
	if c.Seat(p.Target) == nil {
		return fmt.Errorf("unknown target")
	}
	if p.Count <= 0 {
		p.Count = 1
	}
	s.Quota[p.Target] += p.Count
	if s.Current == "" {
		s.Current = p.Target
		if !slices.Contains(s.Order, p.Target) {
			s.Order = append(s.Order, p.Target)
		}
	}
	c.Emit("search.extra", "", engine.Public(), map[string]any{"target": p.Target, "count": p.Count})
	return nil
}

func revealOwn(c *engine.Ctx, pid string, limit int, public bool) {
	s := get(c)
	n := 0
	for _, clue := range c.Script().Clues {
		if n >= limit {
			break
		}
		if s.Owners[clue.ID] == pid && !s.Public[clue.ID] {
			s.Public[clue.ID] = true
			n++
			c.Emit("clue.published", pid, engine.Public(), payload(clue))
		}
	}
}

func exhausted(c *engine.Ctx, s *state) {
	if s.Mode == "pool" {
		remaining := 0
		for _, id := range s.Pool {
			if !s.Drawn[id] && s.Owners[id] == "" {
				remaining++
			}
		}
		for _, seat := range c.Seats() {
			if s.Quota[seat.ID] <= 0 || remaining == 0 {
				ready.Mark(c, seat.ID)
			}
		}
		if remaining == 0 {
			s.Current = ""
		}
		return
	}
	remaining := 0
	for _, loc := range locations(c, s) {
		remaining += loc.Remaining
	}
	for _, seat := range c.Seats() {
		if s.Quota[seat.ID] <= 0 || remaining == 0 {
			ready.Mark(c, seat.ID)
		}
	}
}

func available(c *engine.Ctx, s *state, clue script.Clue) bool {
	if s.Owners[clue.ID] != "" || s.Public[clue.ID] || clue.Visibility == "public" {
		return false
	}
	return eligible(c, s, clue)
}

func eligible(c *engine.Ctx, s *state, clue script.Clue) bool {
	loc := c.Script().Location(clue.Location)
	if clue.Act > act(c) || (loc != nil && loc.Act > act(c)) {
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
	for _, id := range clue.RequiresItems {
		found := false
		for _, items := range s.Items {
			if slices.Contains(items, id) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func act(c *engine.Ctx) int {
	if c.PhaseType() == "reading" || c.PhaseType() == "search" {
		return c.Act()
	}
	n, _ := c.Get("reading.act").(int)
	return n
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

func hostActions(c *engine.Ctx) []engine.ActionSpec {
	var targets []engine.Option
	for _, seat := range c.Seats() {
		targets = append(targets, engine.Option{Value: seat.ID, Label: seat.CharName})
	}
	var clueOpts []engine.Option
	for _, clue := range c.Script().Clues {
		clueOpts = append(clueOpts, engine.Option{Value: clue.ID, Label: clue.Title})
	}
	return []engine.ActionSpec{
		{Type: "host.grant_clue", Desc: "Give a clue", Params: map[string]string{"target": "Player ID", "clue": "Clue ID"}, Options: targets, Optional: true},
		{Type: "host.show_clues", Desc: "Reveal a player's clues", Params: map[string]string{"target": "Player ID"}, Options: targets, Optional: true},
		{Type: "host.extra_pick", Desc: "Grant an extra pick", Params: map[string]string{"target": "Player ID", "count": "Number"}, Options: targets, Optional: true},
		{Type: "host.order", Desc: "Set the search order", Params: map[string]string{"order": "JSON array of player IDs"}, Optional: true},
	}
}

func DistributePrivate(c *engine.Ctx, ids []string) {
	s := get(c)
	seats := []*engine.Seat{}
	for _, seat := range c.Seats() {
		if seat.Joined {
			seats = append(seats, seat)
		}
	}
	if len(seats) == 0 {
		return
	}
	for i, id := range ids {
		clue := c.Script().Clue(id)
		if clue == nil || s.Owners[id] != "" || s.Public[id] {
			continue
		}
		owner := clue.OwnerRoleID
		if owner == "" || c.Seat(owner) == nil {
			owner = seats[i%len(seats)].ID
		}
		s.Owners[id] = owner
		c.Emit("clue.found", owner, engine.To(owner), payload(*clue))
		if clue.GrantsItem != "" {
			s.Items[owner] = append(s.Items[owner], clue.GrantsItem)
			c.Emit("item.granted", owner, engine.To(owner), map[string]any{"item": clue.GrantsItem})
		}
	}
}

func initialOwnerClues(c *engine.Ctx) []string {
	delayed := map[string]bool{}
	for _, step := range c.Script().Flow {
		for _, id := range stringList(step.Params["private_clues"]) {
			delayed[id] = true
		}
	}
	var out []string
	for _, clue := range c.Script().Clues {
		if clue.OwnerRoleID != "" && !delayed[clue.ID] {
			out = append(out, clue.ID)
		}
	}
	return out
}

func firstReading(c *engine.Ctx) bool {
	for _, step := range c.Script().Flow {
		if step.Type == "reading" {
			return step.ID == c.PhaseID()
		}
	}
	return false
}

func payload(clue script.Clue) map[string]any {
	p := map[string]any{"id": clue.ID, "location": clue.Location, "title": clue.Title, "body": clue.Body,
		"act": clue.Act, "visibility": clue.Visibility, "kind": clue.Kind, "number": clue.Number,
		"image_url": clue.ImageURL, "floor": clue.Floor}
	if clue.Keywords != nil {
		p["keywords"] = clue.Keywords
	}
	return p
}

func stringList(v any) []string {
	var out []string
	switch x := v.(type) {
	case []any:
		for _, item := range x {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	case []string:
		out = append(out, x...)
	case string:
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}
