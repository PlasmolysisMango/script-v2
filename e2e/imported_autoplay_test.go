package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"jbs/internal/engine"
	"jbs/internal/plugins"
	"jbs/internal/script"
)

// TestImportedAutoplay drives every imported script through the engine with
// deterministic seat bots and a host that only advances when no seat has a
// compulsory action left. It is an engine-level smoke test, not a rules audit.
func TestImportedAutoplay(t *testing.T) {
	root := os.Getenv("JBS_IMPORT_ROOT")
	if root == "" {
		t.Skip("set JBS_IMPORT_ROOT to the imported scripts directory")
	}
	pkgs := loadImported(t, root)
	if len(pkgs) == 0 {
		t.Fatal("no imported scripts found")
	}
	for _, pkg := range pkgs {
		pkg := pkg
		t.Run(pkg.Meta.ID, func(t *testing.T) {
			checkPackage(t, pkg, root)
			if err := autoplay(t, pkg, 5000); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func checkPackage(t *testing.T, pkg *script.Package, root string) {
	t.Helper()
	switch pkg.Meta.ID {
	case "k19":
		if pkg.CardGame == nil {
			t.Fatal("missing card game")
		}
		if pkg.CardGame.Deal != 27 || len(pkg.CardGame.Cards) != 135 {
			t.Fatalf("card game deal=%d cards=%d, want 27/135", pkg.CardGame.Deal, len(pkg.CardGame.Cards))
		}
	case "k13":
		found := false
		for _, s := range pkg.Flow {
			if s.Type == "dice" {
				found = true
			}
		}
		if !found {
			t.Fatal("missing dice phase")
		}
	case "k12", "k23":
		if len(pkg.Votes) != 7 {
			t.Fatalf("%d votes, want 7", len(pkg.Votes))
		}
	case "k10":
		if len(pkg.Endings) < 6 {
			t.Fatalf("%d endings, want at least 6", len(pkg.Endings))
		}
	case "k18":
		if pkg.KillerKind != "npc" || pkg.KillerName == "" {
			t.Fatalf("killer kind=%q name=%q, want npc", pkg.KillerKind, pkg.KillerName)
		}
	case "aofu":
		audio := ""
		for _, s := range pkg.Flow {
			if audio = s.Str("audio_url", s.Str("audioUrl", "")); audio != "" {
				break
			}
		}
		if audio == "" {
			t.Fatal("missing letter audio")
		}
		asset := filepath.Join(root, "assets", filepath.FromSlash(strings.TrimPrefix(audio, "/script-assets/")))
		if rel, err := filepath.Rel(filepath.Join(root, "assets"), asset); err != nil || strings.HasPrefix(rel, "..") {
			t.Fatalf("audio asset %q escapes assets root", audio)
		}
		if _, err := os.Stat(asset); err != nil {
			t.Fatalf("audio asset %s: %v", asset, err)
		}
		if pkg.Character("r_narrator") == nil {
			t.Fatal("missing narrator role")
		}
	}
}

func loadImported(t *testing.T, root string) []*script.Package {
	t.Helper()
	var out []*script.Package
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "assets" || d.Name() == "node_modules" || d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "script.json" {
			return nil
		}
		pkg, err := script.Load(path)
		if err != nil {
			return fmt.Errorf("load %s: %w", path, err)
		}
		out = append(out, pkg)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Meta.ID < out[j].Meta.ID })
	return out
}

func autoplay(t *testing.T, pkg *script.Package, maxSteps int) error {
	t.Helper()
	reg, err := plugins.NewRegistry()
	if err != nil {
		return err
	}
	room, err := engine.NewRoom("autoplay-"+pkg.Meta.ID, pkg, reg, nil)
	if err != nil {
		return err
	}
	defer room.Close()
	for _, ch := range pkg.Characters {
		if _, err = room.Join(ch.ID, ch.Name, "human"); err != nil {
			return fmt.Errorf("join %s: %w", ch.ID, err)
		}
	}
	seats := make([]string, 0, len(pkg.Characters))
	for _, ch := range pkg.Characters {
		seats = append(seats, ch.ID)
	}
	lastPhase := ""
	rejects := 0
	done := false
	sawMedia := false
	for step := 0; step < maxSteps && !done; step++ {
		info := room.Info()
		if info.Ended {
			break
		}
		if info.Phase != lastPhase {
			lastPhase = info.Phase
			rejects = 0
			for _, pid := range seats {
				v := room.View(pid)
				checkViewPrivacy(t, pkg, pid, v)
				if v.Data["media"] != nil {
					sawMedia = true
				}
			}
		}
		acted := false
		for _, pid := range seats {
			v := room.View(pid)
			if v.Ended {
				done = true
				break
			}
			a, ok := botAction(v, pid)
			if !ok {
				continue
			}
			if err = room.Submit(a); err == nil {
				acted = true
				break
			}
			rejects++
			if rejects > 200 {
				return fmt.Errorf("too many rejected player actions in %q: %v", info.Phase, err)
			}
		}
		if done {
			break
		}
		if acted {
			continue
		}
		hv := room.View(engine.HostID)
		spec, ok := findAction(hv.Allowed, "host.advance")
		if !ok {
			return fmt.Errorf("stalled in %q (type %s): no player or host action", info.Phase, hv.Phase.Type)
		}
		a := engine.Action{Type: spec.Type, Actor: engine.HostID, StateID: hv.StateID, Payload: json.RawMessage(`{}`)}
		if err = room.Submit(a); err != nil {
			return fmt.Errorf("host advance in %q: %w", info.Phase, err)
		}
	}
	if !room.Info().Ended {
		return fmt.Errorf("step budget %d exhausted in %q", maxSteps, room.Info().Phase)
	}
	verifyRun(t, pkg, room, seats, sawMedia)
	verifyPrivacy(t, pkg, room, seats)
	return nil
}

func checkViewPrivacy(t *testing.T, pkg *script.Package, pid string, v engine.View) {
	t.Helper()
	if _, ok := v.Data["host"]; ok {
		t.Fatalf("%s sees host data in %q", pid, v.Phase.Type)
	}
	for _, p := range v.Panels {
		if p.HostOnly {
			t.Fatalf("%s sees host-only panel %q in %q", pid, p.ID, v.Phase.Type)
		}
	}
}

func verifyRun(t *testing.T, pkg *script.Package, room *engine.Room, seats []string, sawMedia bool) {
	t.Helper()
	events := room.Log("*", 0)
	count := func(typ string) int {
		n := 0
		for _, e := range events {
			if e.Type == typ {
				n++
			}
		}
		return n
	}
	var reveal *engine.Event
	for i := range events {
		if events[i].Type == "reveal" {
			reveal = &events[i]
		}
	}
	switch pkg.Meta.ID {
	case "k19":
		deals := 0
		for _, e := range events {
			if e.Type != "card.deal" {
				continue
			}
			deals++
			var p struct {
				Counts map[string]int `json:"counts"`
			}
			b, _ := json.Marshal(e.Payload)
			_ = json.Unmarshal(b, &p)
			if len(p.Counts) != len(seats) {
				t.Fatalf("dealt to %d seats, want %d", len(p.Counts), len(seats))
			}
			for pid, n := range p.Counts {
				if n != 27 {
					t.Fatalf("%s dealt %d cards, want 27", pid, n)
				}
			}
		}
		if deals != 1 {
			t.Fatalf("%d deals, want 1", deals)
		}
		scored := false
		for _, e := range events {
			if e.Type != "card.score" {
				continue
			}
			var p struct {
				Order []string `json:"order"`
			}
			b, _ := json.Marshal(e.Payload)
			_ = json.Unmarshal(b, &p)
			if len(p.Order) != len(seats) {
				t.Fatalf("scored %d players, want %d", len(p.Order), len(seats))
			}
			scored = true
		}
		if !scored {
			t.Fatal("card game never scored")
		}
	case "k13":
		questions, answers, rounds := count("dice.question"), count("dice.answer"), count("dice.round")
		if questions != 20 || answers != 20 || rounds != 20 {
			t.Fatalf("dice rounds=%d questions=%d answers=%d, want 20 each", rounds, questions, answers)
		}
		for _, e := range events {
			if e.Type == "dice.end" {
				var p struct {
					Rounds int `json:"rounds"`
				}
				b, _ := json.Marshal(e.Payload)
				_ = json.Unmarshal(b, &p)
				if p.Rounds != 20 {
					t.Fatalf("dice.end rounds=%d, want 20", p.Rounds)
				}
			}
		}
	case "k12", "k23":
		if n := count("vote.result"); n != 7 {
			t.Fatalf("%d vote results, want 7", n)
		}
	case "k10":
		if reveal == nil {
			t.Fatal("missing reveal")
		}
		title, _ := reveal.Payload["title"].(string)
		found := false
		for _, e := range pkg.Endings {
			if e.Title == title {
				found = true
			}
		}
		if title == "" || !found {
			t.Fatalf("reveal title %q does not match an ending", title)
		}
	case "k18":
		if reveal == nil {
			t.Fatal("missing reveal")
		}
		if title, _ := reveal.Payload["title"].(string); title == "" {
			t.Fatal("missing ending title for npc killer")
		}
	case "aofu":
		if !sawMedia {
			t.Fatal("letter audio never exposed")
		}
		leftover := false
		for _, e := range events {
			if e.Type == "clue.found" && e.Actor == "r_narrator" {
				leftover = true
			}
		}
		if !leftover {
			t.Fatal("no leftover clues assigned to narrator")
		}
	}
}

func verifyPrivacy(t *testing.T, pkg *script.Package, room *engine.Room, seats []string) {
	t.Helper()
	own, private := privateLines(pkg)
	needles := []string{"dm_script", "host_facts", "host_hands"}
	for _, pid := range seats {
		v := room.View(pid)
		checkViewPrivacy(t, pkg, pid, v)
		view, _ := json.Marshal(v)
		checkLeak(t, pid, "view", string(view), needles, own[pid], private)
		logs, _ := json.Marshal(room.Log(pid, 0))
		checkLeak(t, pid, "log", string(logs), needles, own[pid], private)
	}
}

// privateLines returns each character's own rolebook text and the lines that
// are unique to a single rolebook. Lines duplicated across rolebooks, public
// intros, clues, meta, truth, or endings are excluded to avoid false positives.
func privateLines(pkg *script.Package) (map[string]string, map[string][]string) {
	own := map[string]string{}
	lines := map[string]map[string]bool{}
	for _, ch := range pkg.Characters {
		var b strings.Builder
		set := map[string]bool{}
		appendText := func(text string) {
			b.WriteString(text)
			b.WriteString("\n")
			addLines(set, text)
		}
		for _, sec := range ch.Sections {
			appendText(sec.Body)
		}
		appendText(ch.PublicIntro)
		appendText(ch.Performance)
		appendText(ch.Ability)
		appendText(ch.Letter)
		for _, goal := range ch.Goals {
			appendText(goal)
		}
		if ch.Skill != nil {
			appendText(ch.Skill.Text)
		}
		own[ch.ID] = b.String()
		lines[ch.ID] = set
	}
	public := map[string]bool{}
	add := func(text string) {
		for _, line := range sectionLines(text) {
			public[line] = true
		}
	}
	add(pkg.Meta.Intro)
	add(pkg.Meta.Rules)
	add(pkg.Truth)
	for _, ch := range pkg.Characters {
		add(ch.PublicIntro)
	}
	for _, c := range pkg.Clues {
		add(c.Title)
		add(c.Body)
	}
	for _, e := range pkg.Endings {
		add(e.Title)
		add(e.Text)
	}
	for _, s := range pkg.Flow {
		add(s.Title)
		for k, v := range s.Params {
			key := strings.ToLower(k)
			if strings.Contains(key, "dm_script") || strings.Contains(key, "host") || strings.Contains(key, "fact") {
				continue
			}
			if text, ok := v.(string); ok {
				add(text)
			}
		}
	}
	private := map[string][]string{}
	for _, ch := range pkg.Characters {
		for line := range lines[ch.ID] {
			if public[line] {
				continue
			}
			unique := true
			for _, other := range pkg.Characters {
				if other.ID != ch.ID && lines[other.ID][line] {
					unique = false
					break
				}
			}
			if unique {
				private[ch.ID] = append(private[ch.ID], line)
			}
		}
	}
	return own, private
}

func addLines(set map[string]bool, text string) {
	for _, line := range sectionLines(text) {
		set[line] = true
	}
}

func sectionLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if len([]rune(line)) < 24 || strings.ContainsAny(line, "|#`>*\"\\") ||
			strings.Contains(line, "](") || strings.Contains(line, "/script-assets/") {
			continue
		}
		out = append(out, line)
	}
	return out
}

func checkLeak(t *testing.T, pid, kind, s string, needles []string, own string, private map[string][]string) {
	t.Helper()
	for _, n := range needles {
		if strings.Contains(s, n) {
			t.Fatalf("%s %s leaks %q", pid, kind, n)
		}
	}
	for oid, lines := range private {
		if oid == pid {
			continue
		}
		for _, line := range lines {
			if strings.Contains(own, line) {
				continue
			}
			if strings.Contains(s, line) {
				t.Fatalf("%s %s leaks %s private section %q", pid, kind, oid, line)
			}
		}
	}
}

func findAction(specs []engine.ActionSpec, typ string) (engine.ActionSpec, bool) {
	for _, s := range specs {
		if s.Type == typ {
			return s, true
		}
	}
	return engine.ActionSpec{}, false
}

func botAction(v engine.View, pid string) (engine.Action, bool) {
	for _, typ := range []string{"card.play", "card.draw", "card.pass", "dice.ask", "dice.answer", "search", "vote.cast", "vote", "say", "pass", "ready"} {
		s, ok := findAction(v.Allowed, typ)
		if !ok || s.Optional || typ == "points.skill" {
			continue
		}
		payload, ok := botPayload(v, typ, s)
		if !ok {
			continue
		}
		a := engine.NewAction(typ, payload)
		a.Actor, a.StateID = pid, v.StateID
		return a, true
	}
	return engine.Action{}, false
}

func botPayload(v engine.View, typ string, s engine.ActionSpec) (map[string]string, bool) {
	payload := map[string]string{}
	switch typ {
	case "ready", "pass", "card.draw", "card.pass":
	case "say":
		payload["text"] = "我先核对自己掌握的线索，再听听各位的说法。"
	case "search":
		if len(s.Fields) > 0 {
			return payload, true // category is optional; draw randomly
		}
		for k := range s.Params {
			if len(s.Options) > 0 {
				payload[k] = s.Options[0].Value
			}
		}
	case "vote":
		if len(s.Options) == 0 {
			return nil, false
		}
		payload["option"] = s.Options[0].Value
	case "vote.cast":
		question, option := "", ""
		var votes struct {
			Questions []struct {
				ID      string          `json:"id"`
				Options []engine.Option `json:"options"`
			} `json:"questions"`
			Cast map[string]string `json:"cast"`
		}
		b, _ := json.Marshal(v.Data["votes"])
		_ = json.Unmarshal(b, &votes)
		for _, allowed := range s.Options {
			if _, cast := votes.Cast[allowed.Value]; cast {
				continue
			}
			for _, q := range votes.Questions {
				if q.ID == allowed.Value && len(q.Options) > 0 {
					question, option = q.ID, q.Options[0].Value
					break
				}
			}
			if question != "" {
				break
			}
		}
		if question == "" {
			return nil, false
		}
		payload["question"], payload["option"] = question, option
	case "dice.ask":
		if len(s.Options) == 0 {
			return nil, false
		}
		payload["question"] = s.Options[0].Value
	case "dice.answer":
		var dv struct {
			Question string            `json:"question"`
			Keywords map[string]string `json:"keywords"`
		}
		b, _ := json.Marshal(v.Data["dice"])
		_ = json.Unmarshal(b, &dv)
		keys := make([]string, 0, len(dv.Keywords))
		for k := range dv.Keywords {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		text := ""
		for _, k := range keys {
			if strings.Contains(dv.Question, k) {
				text = dv.Keywords[k]
				break
			}
		}
		if text == "" {
			text = "我确认情况属实，并愿意继续交代。"
		}
		payload["text"] = text
	case "card.play":
		var cv struct {
			Playable []struct {
				ID    string `json:"id"`
				Kind  string `json:"kind"`
				Color string `json:"color"`
			} `json:"playable"`
		}
		b, _ := json.Marshal(v.Data["cards"])
		_ = json.Unmarshal(b, &cv)
		if len(cv.Playable) == 0 {
			return nil, false
		}
		payload["card"] = cv.Playable[0].ID
		if cv.Playable[0].Kind == "recolor" {
			for _, f := range s.Fields {
				if f.Key == "color" && len(f.Options) > 0 {
					payload["color"] = f.Options[0].Value
				}
			}
		}
		playable := false
		for _, id := range handIDs(v) {
			if id == payload["card"] {
				playable = true
			}
		}
		if !playable {
			return nil, false
		}
	}
	for _, f := range s.Fields {
		if f.Required {
			if payload[f.Key] == "" {
				return nil, false
			}
		}
	}
	for k := range s.Params {
		if _, ok := payload[k]; !ok {
			return nil, false
		}
	}
	return payload, true
}

func handIDs(v engine.View) []string {
	var cv struct {
		Hand []struct {
			ID string `json:"id"`
		} `json:"hand"`
	}
	b, _ := json.Marshal(v.Data["cards"])
	_ = json.Unmarshal(b, &cv)
	out := make([]string, 0, len(cv.Hand))
	for _, c := range cv.Hand {
		out = append(out, c.ID)
	}
	return out
}
