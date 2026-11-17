package script

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Package struct {
	Meta          Meta        `json:"meta"`
	Characters    []Character `json:"characters"`
	Acts          []Act       `json:"acts"`
	Locations     []Location  `json:"locations"`
	Clues         []Clue      `json:"clues"`
	Flow          []Step      `json:"flow"`
	Votes         []Vote      `json:"votes"`
	Endings       []Ending    `json:"endings"`
	Truth         string      `json:"truth"`
	HostFacts     []Fact      `json:"host_facts,omitempty"`
	TruthCases    []Case      `json:"truth_cases,omitempty"`
	KillerRoleIDs []string    `json:"killer_role_ids,omitempty"`
	KillerKind    string      `json:"killer_kind,omitempty"`
	KillerName    string      `json:"killer_name,omitempty"`
	CardGame      *CardGame   `json:"card_game,omitempty"`
}

type Meta struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Players     int      `json:"players"`
	Lang        string   `json:"lang"`
	Intro       string   `json:"intro"`
	Rules       string   `json:"rules,omitempty"`
	Credits     string   `json:"credits,omitempty"`
	Source      string   `json:"source,omitempty"`
	LicenseNote string   `json:"license_note,omitempty"`
	Series      string   `json:"series,omitempty"`
	Author      string   `json:"author,omitempty"`
	Difficulty  string   `json:"difficulty,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	DurationMin int      `json:"duration_min,omitempty"`
	PointsLabel string   `json:"points_label,omitempty"`
	HidePoints  bool     `json:"hide_points,omitempty"`
	StartPoints int      `json:"start_points,omitempty"`
}

type Fact struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type Case struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	PhaseID       string   `json:"phase_id,omitempty"`
	KillerRoleIDs []string `json:"killer_role_ids,omitempty"`
	Method        string   `json:"method,omitempty"`
	Motive        string   `json:"motive,omitempty"`
}

type CardGame struct {
	Deal       int        `json:"deal"`
	Points     []int      `json:"points"`
	KillerRank int        `json:"killer_rank"`
	Cards      []Card     `json:"cards"`
	Words      []CardWord `json:"words"`
}

type Card struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Color  string `json:"color,omitempty"`
	Number int    `json:"number,omitempty"`
	Title  string `json:"title,omitempty"`
	Text   string `json:"text,omitempty"`
	First  bool   `json:"first,omitempty"`
}

type CardWord struct {
	RoleID string `json:"role_id"`
	Word   string `json:"word"`
	Open   bool   `json:"open,omitempty"`
}

type Voice struct {
	Hint  string  `json:"voice_hint"`
	Rate  float64 `json:"rate,omitempty"`
	Pitch float64 `json:"pitch,omitempty"`
}

type Section struct {
	Act   int    `json:"act"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

type Character struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	PublicIntro string    `json:"public_intro"`
	Gender      string    `json:"gender,omitempty"`
	TTS         Voice     `json:"tts"`
	Sections    []Section `json:"sections"`
	Goals       []string  `json:"goals,omitempty"`
	Performance string    `json:"performance,omitempty"`
	Ability     string    `json:"ability,omitempty"`
	CanLie      bool      `json:"can_lie"`
	IsCulprit   bool      `json:"is_culprit"`
	Points      int       `json:"points,omitempty"`
	Floor       string    `json:"floor,omitempty"`
	Letter      string    `json:"letter,omitempty"`
	Skill       *Skill    `json:"skill,omitempty"`
}

type Skill struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Text         string `json:"text"`
	Effect       string `json:"effect"`
	Cost         int    `json:"cost,omitempty"`
	ExactCost    int    `json:"exact_cost,omitempty"`
	AllCost      bool   `json:"all_cost,omitempty"`
	Value        int    `json:"value,omitempty"`
	Target       string `json:"target,omitempty"`
	TargetRoleID string `json:"target_role_id,omitempty"`
	Public       bool   `json:"public,omitempty"`
}

type Act struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
}

type Location struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Act  int    `json:"act"`
}

type Unlock struct {
	RequiresClues []string `json:"requires_clues,omitempty"`
	RequiresPhase string   `json:"requires_phase,omitempty"`
}

type Clue struct {
	ID            string   `json:"id"`
	Location      string   `json:"location,omitempty"`
	Title         string   `json:"title,omitempty"`
	Body          string   `json:"body"`
	Act           int      `json:"act"`
	Visibility    string   `json:"visibility"` // private | public
	Unlock        *Unlock  `json:"unlock,omitempty"`
	Kind          string   `json:"kind,omitempty"`
	Color         string   `json:"color,omitempty"`
	Back          string   `json:"back,omitempty"`
	Floor         string   `json:"floor,omitempty"`
	ImageURL      string   `json:"image_url,omitempty"`
	OwnerRoleID   string   `json:"owner_role_id,omitempty"`
	SubjectRoleID string   `json:"subject_role_id,omitempty"`
	Keywords      []string `json:"keywords,omitempty"`
	KeywordsAll   bool     `json:"keywords_all,omitempty"`
	MustReveal    bool     `json:"must_reveal,omitempty"`
	RequiresItems []string `json:"requires_items,omitempty"`
	UnlockedBy    []string `json:"unlocked_by,omitempty"`
	GrantsItem    string   `json:"grants_item,omitempty"`
	GrantsClue    []string `json:"grants_clue,omitempty"`
	ExtraDraw     bool     `json:"extra_draw,omitempty"`
	Number        int      `json:"number,omitempty"`
}

type Step struct {
	ID     string         `json:"id"`
	Type   string         `json:"type"`
	Title  string         `json:"title"`
	Params map[string]any `json:"params,omitempty"`
	Next   string         `json:"next,omitempty"`
}

func (s Step) Int(key string, def int) int {
	switch v := s.Params[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return def
}

func (s Step) Str(key, def string) string {
	if v, ok := s.Params[key].(string); ok {
		return v
	}
	return def
}

func (s Step) Bool(key string, def bool) bool {
	if v, ok := s.Params[key].(bool); ok {
		return v
	}
	return def
}

type Vote struct {
	ID           string       `json:"id"`
	Question     string       `json:"question"`
	Options      string       `json:"options"` // "characters", "options", "characters+abstain"
	OptionList   []VoteOption `json:"option_list,omitempty"`
	Correct      string       `json:"correct,omitempty"`
	Corrects     []string     `json:"corrects,omitempty"`
	AllowAbstain bool         `json:"allow_abstain,omitempty"`
	PhaseID      string       `json:"phase_id,omitempty"`
}

type VoteOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type Ending struct {
	When  map[string]string `json:"when"` // vote id -> "correct" | "wrong" | "*" | "winner:<value>"
	Title string            `json:"title"`
	Text  string            `json:"text"`
	ID    string            `json:"id,omitempty"`
}

func Load(path string) (*Package, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		path = filepath.Join(path, "script.json")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

func Parse(b []byte) (*Package, error) {
	var p Package
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("parse script: %w", err)
	}
	p.applyDefaults()
	return &p, p.Validate()
}

func (p *Package) applyDefaults() {
	if p.Meta.Players == 0 {
		p.Meta.Players = len(p.Characters)
	}
	for i := range p.Clues {
		if p.Clues[i].Act == 0 && len(p.Acts) <= 1 {
			p.Clues[i].Act = 1
		}
		if p.Clues[i].Visibility == "" {
			p.Clues[i].Visibility = "private"
		}
	}
	for i := range p.Locations {
		if p.Locations[i].Act == 0 && len(p.Acts) <= 1 {
			p.Locations[i].Act = 1
		}
	}
}

func (p *Package) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "script.json"), b, 0o644)
}

func (p *Package) Validate() error {
	var errs []error
	bad := func(f string, a ...any) { errs = append(errs, fmt.Errorf(f, a...)) }
	if p.Meta.ID == "" || p.Meta.Title == "" {
		bad("meta.id and meta.title are required")
	}
	if len(p.Characters) == 0 {
		bad("no characters")
	}
	acts := map[int]bool{}
	for _, a := range p.Acts {
		if a.ID <= 0 || acts[a.ID] {
			bad("act id %d non-positive or duplicated", a.ID)
		}
		acts[a.ID] = true
	}
	chars := map[string]bool{}
	for _, c := range p.Characters {
		if c.ID == "" || chars[c.ID] {
			bad("character id %q empty or duplicated", c.ID)
		}
		chars[c.ID] = true
		for _, s := range c.Sections {
			if (s.Act != 0 || len(p.Acts) > 1) && !acts[s.Act] {
				bad("character %s: section %q has unknown act %d", c.ID, s.Title, s.Act)
			}
		}
	}
	locs := map[string]bool{}
	for _, l := range p.Locations {
		if l.ID == "" || locs[l.ID] {
			bad("location id %q empty or duplicated", l.ID)
		}
		locs[l.ID] = true
		if (l.Act != 0 || len(p.Acts) > 1) && !acts[l.Act] {
			bad("location %s: unknown act %d", l.ID, l.Act)
		}
	}
	clues := map[string]bool{}
	for _, c := range p.Clues {
		if c.ID == "" || clues[c.ID] {
			bad("clue id %q empty or duplicated", c.ID)
		}
		clues[c.ID] = true
		if (c.Act != 0 || len(p.Acts) > 1) && !acts[c.Act] {
			bad("clue %s: unknown act %d", c.ID, c.Act)
		}
		if !locs[c.Location] && c.Location != "" {
			bad("clue %s: unknown location %q", c.ID, c.Location)
		}
		if c.Visibility != "private" && c.Visibility != "public" {
			bad("clue %s: bad visibility %q", c.ID, c.Visibility)
		}
	}
	items := map[string]bool{}
	for _, c := range p.Clues {
		if c.GrantsItem != "" {
			items[c.GrantsItem] = true
		}
	}
	steps := map[string]bool{}
	for _, s := range p.Flow {
		if s.ID == "" || steps[s.ID] {
			bad("flow step id %q empty or duplicated", s.ID)
		}
		steps[s.ID] = true
		if s.Type == "" {
			bad("flow step %s: missing type", s.ID)
		}
		if v, ok := s.Params["act"]; ok {
			act := s.Int("act", 0)
			if n, ok := v.(float64); ok && n != float64(act) {
				bad("flow step %s: invalid act %v", s.ID, v)
			} else if !acts[act] {
				bad("flow step %s: unknown act %v", s.ID, v)
			}
		} else if len(p.Acts) > 1 && (s.Type == "reading" || s.Type == "search") {
			bad("flow step %s: missing act", s.ID)
		}
	}
	if len(p.Flow) == 0 {
		bad("empty flow")
	}
	for _, s := range p.Flow {
		if s.Next != "" && !steps[s.Next] {
			bad("flow step %s: next %q not found", s.ID, s.Next)
		}
	}
	for _, c := range p.Clues {
		if c.Unlock == nil {
			continue
		}
		for _, r := range c.Unlock.RequiresClues {
			if !clues[r] {
				bad("clue %s: requires unknown clue %q", c.ID, r)
			}
		}
		if c.Unlock.RequiresPhase != "" && !steps[c.Unlock.RequiresPhase] {
			bad("clue %s: requires unknown phase %q", c.ID, c.Unlock.RequiresPhase)
		}
	}
	for _, c := range p.Clues {
		if c.OwnerRoleID != "" && !chars[c.OwnerRoleID] {
			bad("clue %s: unknown owner role %q", c.ID, c.OwnerRoleID)
		}
		if c.SubjectRoleID != "" && !chars[c.SubjectRoleID] {
			bad("clue %s: unknown subject role %q", c.ID, c.SubjectRoleID)
		}
		for _, id := range c.UnlockedBy {
			if !clues[id] {
				bad("clue %s: unlocked by unknown clue %q", c.ID, id)
			}
		}
		for _, id := range c.GrantsClue {
			if !clues[id] {
				bad("clue %s: grants unknown clue %q", c.ID, id)
			}
		}
		for _, item := range c.RequiresItems {
			if !items[item] {
				bad("clue %s: requires unknown item %q", c.ID, item)
			}
		}
	}
	votes := map[string]bool{}
	for _, v := range p.Votes {
		if v.ID == "" || votes[v.ID] {
			bad("vote id %q empty or duplicated", v.ID)
		}
		votes[v.ID] = true
		corrects := append([]string{}, v.Corrects...)
		if v.Correct != "" {
			corrects = append(corrects, v.Correct)
		}
		for _, correct := range corrects {
			if v.Options == "characters" && !chars[correct] {
				bad("vote %s: correct answer %q is not a character", v.ID, correct)
			}
			if len(v.OptionList) > 0 {
				found := false
				for _, o := range v.OptionList {
					if o.Value == correct {
						found = true
					}
				}
				if !found {
					bad("vote %s: correct answer %q is not an option", v.ID, correct)
				}
			}
		}
	}
	for _, tc := range p.TruthCases {
		for _, id := range tc.KillerRoleIDs {
			if !chars[id] {
				bad("case %s: unknown killer role %q", tc.ID, id)
			}
		}
		if tc.PhaseID != "" && !steps[tc.PhaseID] {
			bad("case %s: unknown phase %q", tc.ID, tc.PhaseID)
		}
	}
	for _, id := range p.KillerRoleIDs {
		if !chars[id] {
			bad("truth: unknown killer role %q", id)
		}
	}
	for _, e := range p.Endings {
		for k := range e.When {
			if !votes[k] {
				bad("ending %q: unknown vote %q", e.Title, k)
			}
		}
	}
	return errors.Join(errs...)
}

func (p *Package) Character(id string) *Character {
	for i := range p.Characters {
		if p.Characters[i].ID == id {
			return &p.Characters[i]
		}
	}
	return nil
}

func (p *Package) Clue(id string) *Clue {
	for i := range p.Clues {
		if p.Clues[i].ID == id {
			return &p.Clues[i]
		}
	}
	return nil
}

func (p *Package) Location(id string) *Location {
	for i := range p.Locations {
		if p.Locations[i].ID == id {
			return &p.Locations[i]
		}
	}
	return nil
}

func (p *Package) StepIndex(id string) int {
	for i, s := range p.Flow {
		if s.ID == id {
			return i
		}
	}
	return -1
}
