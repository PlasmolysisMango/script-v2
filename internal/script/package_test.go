package script

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateReferences(t *testing.T) {
	p := &Package{Meta: Meta{ID: "mini", Title: "Mini"}, Characters: []Character{{ID: "a"}}, Flow: []Step{{ID: "start", Type: "reading", Next: "missing"}}}
	if p.Validate() == nil {
		t.Fatal("missing next not rejected")
	}
	p.Flow[0].Next = ""
	p.Locations = []Location{{ID: "room"}}
	p.Clues = []Clue{{ID: "c", Location: "room", Visibility: "private", Unlock: &Unlock{RequiresClues: []string{"missing"}}}}
	if p.Validate() == nil {
		t.Fatal("missing clue not rejected")
	}
	p.Clues[0].Unlock = nil
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}

func multiActPackage() *Package {
	return &Package{
		Meta: Meta{ID: "multi", Title: "Multi-act mystery", Players: 2},
		Characters: []Character{
			{ID: "a", Sections: []Section{{Act: 1, Title: "Opening", Body: "A's opening"}, {Act: 3, Title: "Return", Body: "A's return"}}},
			{ID: "b", Sections: []Section{{Act: 1, Title: "Opening", Body: "B's opening"}, {Act: 3, Title: "Return", Body: "B's return"}}},
		},
		Acts:      []Act{{ID: 1, Title: "Opening"}, {ID: 3, Title: "Return"}},
		Locations: []Location{{ID: "room", Act: 1}, {ID: "garden", Act: 3}},
		Clues: []Clue{
			{ID: "key", Location: "room", Act: 1, Visibility: "private"},
			{ID: "note", Location: "garden", Act: 3, Visibility: "public", Unlock: &Unlock{RequiresClues: []string{"key"}, RequiresPhase: "reading3"}},
		},
		Flow: []Step{
			{ID: "reading1", Type: "reading", Params: map[string]any{"act": 1}, Next: "search1"},
			{ID: "search1", Type: "search", Params: map[string]any{"act": 1}},
			{ID: "custom", Type: "custom", Params: map[string]any{"rounds": 2}},
			{ID: "reading3", Type: "reading", Params: map[string]any{"act": 3}},
			{ID: "search3", Type: "search", Params: map[string]any{"act": 3}},
			{ID: "vote", Type: "vote"},
			{ID: "end", Type: "end"},
		},
		Votes:   []Vote{{ID: "culprit", Options: "characters", Correct: "a"}, {ID: "witness", Options: "characters", Correct: "b"}},
		Endings: []Ending{{Title: "Solved", When: map[string]string{"culprit": "correct", "witness": "correct"}}},
	}
}

func TestValidateMultiActPackage(t *testing.T) {
	p := multiActPackage()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Flow[3].Int("act", 0) != 3 || parsed.Clues[1].Act != 3 || parsed.Characters[1].Sections[1].Act != 3 {
		t.Fatal("multi-act references changed during parsing")
	}
}

func TestValidateVoteIDs(t *testing.T) {
	for _, id := range []string{"", "culprit"} {
		t.Run("id="+id, func(t *testing.T) {
			p := multiActPackage()
			p.Votes[1].ID = id
			if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "vote id") {
				t.Fatalf("invalid vote id %q accepted: %v", id, err)
			}
			b, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Parse(b); err == nil || !strings.Contains(err.Error(), "vote id") {
				t.Fatalf("invalid vote id %q accepted by Parse: %v", id, err)
			}
		})
	}
}

func TestValidateActs(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Package)
		want   string
	}{
		{"zero id", func(p *Package) { p.Acts[0].ID = 0 }, "act id 0"},
		{"negative id", func(p *Package) { p.Acts[0].ID = -1 }, "act id -1"},
		{"duplicate id", func(p *Package) { p.Acts[1].ID = 1 }, "act id 1"},
		{"section unknown", func(p *Package) { p.Characters[1].Sections[1].Act = 2 }, "character b: section"},
		{"section missing", func(p *Package) { p.Characters[1].Sections[1].Act = 0 }, "character b: section"},
		{"section negative", func(p *Package) { p.Characters[0].Sections[0].Act = -1 }, "character a: section"},
		{"location unknown", func(p *Package) { p.Locations[1].Act = 2 }, "location garden: unknown act 2"},
		{"location missing", func(p *Package) { p.Locations[1].Act = 0 }, "location garden: unknown act 0"},
		{"location negative", func(p *Package) { p.Locations[0].Act = -1 }, "location room: unknown act -1"},
		{"clue unknown", func(p *Package) { p.Clues[1].Act = 2 }, "clue note: unknown act 2"},
		{"clue missing", func(p *Package) { p.Clues[1].Act = 0 }, "clue note: unknown act 0"},
		{"clue negative", func(p *Package) { p.Clues[0].Act = -1 }, "clue key: unknown act -1"},
		{"reading unknown", func(p *Package) { p.Flow[0].Params["act"] = 2 }, "flow step reading1: unknown act 2"},
		{"search unknown", func(p *Package) { p.Flow[1].Params["act"] = 2 }, "flow step search1: unknown act 2"},
		{"reading missing", func(p *Package) { p.Flow[0].Params = nil }, "flow step reading1: missing act"},
		{"search missing", func(p *Package) { delete(p.Flow[1].Params, "act") }, "flow step search1: missing act"},
		{"phase zero", func(p *Package) { p.Flow[0].Params["act"] = 0 }, "flow step reading1: unknown act 0"},
		{"phase negative", func(p *Package) { p.Flow[0].Params["act"] = -1 }, "flow step reading1: unknown act -1"},
		{"phase fractional", func(p *Package) { p.Flow[0].Params["act"] = 1.5 }, "flow step reading1: invalid act 1.5"},
		{"phase string", func(p *Package) { p.Flow[0].Params["act"] = "1" }, "flow step reading1: unknown act"},
		{"phase null", func(p *Package) { p.Flow[0].Params["act"] = nil }, "flow step reading1: unknown act"},
		{"custom explicit unknown", func(p *Package) { p.Flow[2].Params["act"] = 2 }, "flow step custom: unknown act 2"},
		{"undeclared acts", func(p *Package) { p.Acts = nil }, "unknown act"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := multiActPackage()
			tc.change(p)
			if err := p.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate error = %v, want %q", err, tc.want)
			}
			b, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Parse(b); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateCustomPhasesWithoutActs(t *testing.T) {
	p := &Package{
		Meta:       Meta{ID: "custom", Title: "Custom game"},
		Characters: []Character{{ID: "a"}},
		Flow:       []Step{{ID: "custom", Type: "custom", Params: map[string]any{"rounds": 2}}, {ID: "end", Type: "end"}},
	}
	for _, acts := range [][]Act{nil, {{ID: 1}}, {{ID: 1}, {ID: 3}}} {
		p.Acts = acts
		if err := p.Validate(); err != nil {
			t.Fatalf("custom phases without act references rejected: %v", err)
		}
	}
}

func TestParseSingleActDefaults(t *testing.T) {
	p, err := Parse([]byte(`{
		"meta":{"id":"single","title":"Single act"},
		"characters":[{"id":"a","sections":[{"act":1,"body":"Opening"}]}],
		"acts":[{"id":1}],
		"locations":[{"id":"room"}],
		"clues":[{"id":"key","location":"room"}],
		"flow":[{"id":"reading","type":"reading"},{"id":"search","type":"search"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Locations[0].Act != 1 || p.Clues[0].Act != 1 || p.Clues[0].Visibility != "private" {
		t.Fatal("single-act defaults lost")
	}
}
