package points

import (
	"fmt"
	"sort"

	"jbs/internal/engine"
	"jbs/internal/script"
)

type Plugin struct{}

type State struct {
	Points        map[string]int  `json:"points"`
	Label         string          `json:"label"`
	Hide          bool            `json:"hide"`
	Settled       bool            `json:"settled"`
	Throne        string          `json:"throne,omitempty"`
	LastPenalized string          `json:"last_penalized,omitempty"`
	UsedSkills    map[string]bool `json:"used_skills,omitempty"`
	ExtraDraws    map[string]int  `json:"extra_draws,omitempty"`
}

type RankEntry struct {
	PID    string `json:"pid"`
	Name   string `json:"name"`
	Points int    `json:"points"`
}

type View struct {
	Points  int         `json:"points"`
	Label   string      `json:"label"`
	Skills  []SkillView `json:"skills,omitempty"`
	Rank    []RankEntry `json:"rank,omitempty"`
	Throne  string      `json:"throne,omitempty"`
	Settled bool        `json:"settled"`
	Host    []RankEntry `json:"host,omitempty"`
}

type SkillView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Text    string `json:"text"`
	Effect  string `json:"effect"`
	Cost    int    `json:"cost,omitempty"`
	Enabled bool   `json:"enabled"`
}

func (Plugin) Name() string { return "points" }
func (Plugin) Init(r *engine.Registry) error {
	r.Action("points.skill", skill)
	r.Action("host.points", func(c *engine.Ctx, a engine.Action) error {
		if a.Actor != engine.HostID {
			return engine.ErrNotAllowed
		}
		var p struct {
			Target string `json:"target"`
			Delta  int    `json:"delta"`
		}
		if err := a.Decode(&p); err != nil {
			return err
		}
		if c.Seat(p.Target) == nil {
			return fmt.Errorf("unknown target %q", p.Target)
		}
		adjust(c, p.Target, p.Delta)
		return nil
	})
	r.Action("host.settle", func(c *engine.Ctx, a engine.Action) error {
		if a.Actor != engine.HostID {
			return engine.ErrNotAllowed
		}
		settle(c)
		return nil
	})
	r.Action("host.throne", func(c *engine.Ctx, a engine.Action) error {
		if a.Actor != engine.HostID {
			return engine.ErrNotAllowed
		}
		var p struct {
			Target string `json:"target"`
		}
		if err := a.Decode(&p); err != nil {
			return err
		}
		if c.Seat(p.Target) == nil {
			return fmt.Errorf("unknown target %q", p.Target)
		}
		s := Init(c)
		s.Throne = p.Target
		c.Emit("points.throne", a.Actor, engine.Public(), map[string]any{"target": p.Target})
		return nil
	})
	r.Allowed(func(c *engine.Ctx, pid string) []engine.ActionSpec {
		ch := c.Script().Character(pid)
		if ch == nil || ch.Skill == nil {
			return nil
		}
		s := Init(c)
		if s.UsedSkills[ch.Skill.ID] || !canAfford(s, pid, ch.Skill) {
			return nil
		}
		spec := engine.ActionSpec{Type: "points.skill", Desc: ch.Skill.Name,
			Fields: []engine.Field{{Key: "skill", Label: ch.Skill.Name, Kind: "select", Required: true,
				Options: []engine.Option{{Value: ch.Skill.ID, Label: ch.Skill.Name}}}}}
		if ch.Skill.Target == "other" || ch.Skill.Target == "next" {
			var opts []engine.Option
			for _, seat := range c.Seats() {
				if seat.Joined && seat.ID != pid {
					opts = append(opts, engine.Option{Value: seat.ID, Label: seat.CharName})
				}
			}
			spec.Fields = append(spec.Fields, engine.Field{Key: "target", Label: "Target", Kind: "select",
				Options: opts, Required: ch.Skill.Target == "other"})
		}
		return []engine.ActionSpec{spec}
	})
	r.Allowed(func(c *engine.Ctx, pid string) []engine.ActionSpec {
		if pid != engine.HostID {
			return nil
		}
		opts := make([]engine.Option, 0, len(c.Seats()))
		for _, s := range c.Seats() {
			opts = append(opts, engine.Option{Value: s.ID, Label: s.CharName})
		}
		return []engine.ActionSpec{
			{Type: "host.points", Desc: "Adjust points", Params: map[string]string{"target": "Player ID", "delta": "Integer delta"}, Options: opts, Optional: true},
			{Type: "host.settle", Desc: "Reveal the points ranking", Optional: true},
			{Type: "host.throne", Desc: "Assign the throne", Params: map[string]string{"target": "Player ID"}, Options: opts, Optional: true},
		}
	})
	r.View(func(c *engine.Ctx, pid string) (string, any) {
		s := Init(c)
		v := View{Points: s.Points[pid], Label: s.Label, Settled: s.Settled}
		if pid == engine.HostID {
			v.Host = ranking(c, s)
			for _, entry := range v.Host {
				if s.Throne == entry.PID {
					v.Throne = entry.PID
				}
			}
			return "points", v
		}
		if ch := c.Script().Character(pid); ch != nil && ch.Skill != nil {
			v.Skills = append(v.Skills, SkillView{ID: ch.Skill.ID, Name: ch.Skill.Name, Text: ch.Skill.Text,
				Effect: ch.Skill.Effect, Cost: ch.Skill.Cost, Enabled: !s.UsedSkills[ch.Skill.ID] && canAfford(s, pid, ch.Skill)})
		}
		if s.Settled {
			v.Rank = ranking(c, s)
			v.Throne = s.Throne
		}
		return "points", v
	})
	r.Panel(engine.Panel{ID: "points", Plugin: "points", Kind: "points", Title: "Points", DataKey: "points", Order: 25})
	return nil
}

func Init(c *engine.Ctx) *State {
	if s, _ := c.Get("points.state").(*State); s != nil {
		return s
	}
	s := &State{Points: map[string]int{}, Label: c.Script().Meta.PointsLabel, Hide: c.Script().Meta.HidePoints,
		UsedSkills: map[string]bool{}, ExtraDraws: map[string]int{}}
	if s.Label == "" {
		s.Label = "积分"
	}
	start := c.Script().Meta.StartPoints
	for _, ch := range c.Script().Characters {
		s.Points[ch.ID] = start + ch.Points
	}
	if throne := c.Step().Str("throne_role_id", ""); throne != "" {
		s.Throne = throne
	}
	c.Set("points.state", s)
	return s
}

func Get(c *engine.Ctx) *State { return Init(c) }

func canAfford(s *State, pid string, sk *script.Skill) bool {
	if sk.AllCost {
		return s.Points[pid] > 0
	}
	if sk.ExactCost > 0 && s.Points[pid] != sk.ExactCost {
		return false
	}
	cost := sk.Cost
	if sk.ExactCost > 0 {
		cost = sk.ExactCost
	}
	return s.Points[pid] >= cost
}

func settle(c *engine.Ctx) {
	s := Init(c)
	s.Settled = true
	if s.Throne == "" {
		if r := Rank(c); len(r) > 0 {
			s.Throne = r[0]
		}
	}
	c.Emit("points.settle", "", engine.Public(), map[string]any{"rank": ranking(c, s), "throne": s.Throne})
}

func Settle(c *engine.Ctx) { settle(c) }

func Adjust(c *engine.Ctx, pid string, delta int) { adjust(c, pid, delta) }

func adjust(c *engine.Ctx, pid string, delta int) {
	s := Init(c)
	s.Points[pid] += delta
	c.Emit("points.update", "", engine.Public(), map[string]any{"target": pid, "delta": delta})
}

func Rank(c *engine.Ctx) []string {
	s := Init(c)
	entries := ranking(c, s)
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.PID)
	}
	return out
}

func ranking(c *engine.Ctx, s *State) []RankEntry {
	out := []RankEntry{}
	for _, seat := range c.Seats() {
		out = append(out, RankEntry{PID: seat.ID, Name: seat.CharName, Points: s.Points[seat.ID]})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Points > out[j].Points })
	return out
}

func skill(c *engine.Ctx, a engine.Action) error {
	ch := c.Script().Character(a.Actor)
	if ch == nil || ch.Skill == nil {
		return engine.ErrNotAllowed
	}
	var p struct {
		Skill  string `json:"skill"`
		Target string `json:"target"`
	}
	if err := a.Decode(&p); err != nil {
		return err
	}
	if p.Skill != ch.Skill.ID {
		return fmt.Errorf("unknown skill %q", p.Skill)
	}
	s := Init(c)
	if s.UsedSkills[ch.Skill.ID] {
		return fmt.Errorf("skill already used")
	}
	sk := ch.Skill
	cost := sk.Cost
	if sk.AllCost {
		cost = s.Points[a.Actor]
	}
	if sk.ExactCost > 0 {
		if s.Points[a.Actor] != sk.ExactCost {
			return fmt.Errorf("skill requires exactly %d points", sk.ExactCost)
		}
		cost = sk.ExactCost
	}
	if s.Points[a.Actor] < cost {
		return fmt.Errorf("not enough points")
	}
	target := p.Target
	if sk.TargetRoleID != "" {
		target = sk.TargetRoleID
	}
	if target != "" && c.Seat(target) == nil {
		return fmt.Errorf("unknown target %q", target)
	}
	if sk.Target == "other" && (target == "" || target == a.Actor) {
		return fmt.Errorf("choose another player")
	}
	if sk.Target == "next" && target == "" {
		seats := c.Seats()
		for i, seat := range seats {
			if seat.ID == a.Actor {
				target = seats[(i+1)%len(seats)].ID
				break
			}
		}
	}
	s.Points[a.Actor] -= cost
	s.UsedSkills[sk.ID] = true
	payload := map[string]any{"actor": a.Actor, "skill": sk.ID, "effect": sk.Effect, "cost": cost, "target": target}
	switch sk.Effect {
	case "burn_points":
		if target != "" {
			s.Points[target] = 0
		}
	case "grant_points":
		value := sk.Value
		if value == 0 {
			value = 1
		}
		if target != "" {
			s.Points[target] += value
		}
	case "steal_draws":
		if target != "" {
			s.ExtraDraws[a.Actor]++
			s.ExtraDraws[target]--
		}
	case "show_clues":
		value := sk.Value
		if value == 0 {
			value = 1000
		}
		c.Emit("points.show_clues", a.Actor, engine.Public(), map[string]any{"target": target, "count": value, "public": sk.Public})
	default:
		return fmt.Errorf("unsupported skill effect %q", sk.Effect)
	}
	s.Points[a.Actor] = max(0, s.Points[a.Actor])
	c.Emit("points.skill", a.Actor, engine.Public(), payload)
	return nil
}

func ExtraDraws(c *engine.Ctx, pid string) int {
	s := Init(c)
	n := s.ExtraDraws[pid]
	delete(s.ExtraDraws, pid)
	return n
}

func LastPenalized(c *engine.Ctx) string {
	s := Init(c)
	return s.LastPenalized
}

func SetLastPenalized(c *engine.Ctx, pid string) {
	s := Init(c)
	s.LastPenalized = pid
}

func SetThrone(c *engine.Ctx, pid string) {
	s := Init(c)
	s.Throne = pid
}
