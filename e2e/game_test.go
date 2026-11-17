//go:build e2e

package e2e

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"jbs/internal/engine"
	"jbs/internal/script"
)

func TestCompletedGame(t *testing.T) {
	room := os.Getenv("E2E_ROOM")
	if room == "" {
		t.Skip("set E2E_ROOM to a completed room id")
	}
	if strings.ContainsAny(room, "/\\.") {
		t.Fatal("unsafe room id")
	}
	base := os.Getenv("E2E_DATA")
	if base == "" {
		base = "../data"
	}
	p, err := script.Load("../scripts/eastern-star")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filepath.Join(base, "rooms", room, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var evs []engine.Event
	scan := bufio.NewScanner(file)
	scan.Buffer(make([]byte, 4096), 4<<20)
	for scan.Scan() {
		var e engine.Event
		if err = json.Unmarshal(scan.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		evs = append(evs, e)
	}
	if err = scan.Err(); err != nil {
		t.Fatal(err)
	}
	if len(evs) == 0 {
		t.Fatal("empty game log")
	}
	phases := []string{}
	sayCount := map[string]int{}
	type speechTurn struct {
		phase string
		round int
		pid   string
	}
	speechTurns := map[speechTurn]int{}
	ttsCount := map[string]int{}
	joined := map[string]bool{}
	votes := 0
	ended := false
	aiCalls, fallbacks := 0, 0
	expectedProvider := os.Getenv("E2E_LLM")
	if expectedProvider == "qoder" {
		expectedProvider = "qodercli"
	}
	expectedAI := map[string]bool{}
	for _, pid := range strings.Split(os.Getenv("E2E_AI_SEATS"), ",") {
		if pid = strings.TrimSpace(pid); pid != "" {
			expectedAI[pid] = true
		}
	}
	checkComposition := os.Getenv("E2E_AI_SEATS") != ""
	kinds := map[string]string{}
	sources := map[int64]engine.Event{}
	pairedTTS := map[int64]int{}
	for _, e := range evs {
		sources[e.Seq] = e
	}
	cast := map[string]map[string]string{}
	results := map[string]bool{}
	unlocksFound := 0
	phase := ""
	visited := map[string]bool{}
	owners := map[string]string{}
	publicClues := map[string]bool{}
	searchCounts := map[string]map[string]int{}
	for i, e := range evs {
		if e.Seq != int64(i+1) {
			t.Fatal("sequence gap", e.Seq)
		}
		switch e.Type {
		case "phase.enter":
			phase = e.Str("id")
			visited[phase] = true
			phases = append(phases, phase)
		case "player.join":
			if joined[e.Actor] || p.Character(e.Actor) == nil {
				t.Fatal("invalid or duplicate joined seat", e.Actor)
			}
			joined[e.Actor] = true
			kinds[e.Actor] = e.Str("kind")
			if checkComposition {
				want := "human"
				if expectedAI[e.Actor] {
					want = "ai"
				}
				if kinds[e.Actor] != want {
					t.Fatal("incorrect AI/human composition", e.Actor, kinds[e.Actor], "want", want)
				}
			}
		case "chat.say":
			sayCount[e.Actor]++
			round, _ := e.Payload["round"].(float64)
			speechTurns[speechTurn{phase, int(round), e.Actor}]++
			if !e.Public || !joined[e.Actor] {
				t.Fatal("invalid public speech actor")
			}
		case "clue.found":
			id := e.Str("id")
			clue := p.Clue(id)
			if clue == nil || owners[id] != "" || publicClues[id] {
				t.Fatal("invalid or duplicate clue", id)
			}
			if e.Public || len(e.To) != 1 || e.To[0] != e.Actor {
				t.Fatal("private clue exposed", id)
			}
			if clue.Unlock != nil {
				unlocksFound++
				for _, key := range clue.Unlock.RequiresClues {
					if owners[key] == "" && !publicClues[key] {
						t.Fatal("clue unlocked without prerequisite", id, key)
					}
				}
				if req := clue.Unlock.RequiresPhase; req != "" && !visited[req] {
					t.Fatal("clue unlocked before phase", id, req)
				}
			}
			if searchCounts[phase] == nil {
				searchCounts[phase] = map[string]int{}
			}
			searchCounts[phase][e.Actor]++
			step := p.Flow[p.StepIndex(phase)]
			used := searchCounts[phase][e.Actor]
			if step.Type != "search" || used > step.Int("action_points", 3) {
				t.Fatal("search AP limit exceeded")
			}
			if ap, _ := e.Payload["ap"].(float64); int(ap) != step.Int("action_points", 3)-used {
				t.Fatal("incorrect AP ledger")
			}
			owners[id] = e.Actor
		case "clue.public":
			publicClues[e.Str("id")] = true
		case "clue.published":
			if owners[e.Str("id")] != e.Actor {
				t.Fatal("publishing another player's clue")
			}
			publicClues[e.Str("id")] = true
		case "tts":
			ttsCount[e.Actor]++
			seq, ok := e.Payload["source_seq"].(float64)
			source, found := sources[int64(seq)]
			if !ok || !found || source.Seq >= e.Seq || (source.Type != "chat.say" && source.Type != "narration" && source.Type != "reveal") {
				t.Fatal("invalid TTS source sequence", e.Seq)
			}
			text := source.Str("text")
			if source.Type == "reveal" {
				text = strings.TrimSpace(source.Str("truth") + "\n\n" + text)
			}
			if e.Str("text") != text || e.Str("source") != source.Type || e.Actor != source.Actor || e.Public != source.Public || !reflect.DeepEqual(e.To, source.To) {
				t.Fatal("TTS differs from source", source.Seq)
			}
			pairedTTS[source.Seq]++
		case "vote.cast":
			votes++
			if phase != "vote" || e.Public || len(e.To) != 1 || e.To[0] != e.Actor || !joined[e.Actor] {
				t.Fatal("invalid private vote audience or phase")
			}
			question, option := e.Str("question"), e.Str("option")
			if !slices.ContainsFunc(p.Votes, func(q script.Vote) bool { return q.ID == question }) || p.Character(option) == nil {
				t.Fatal("illegal vote question or option")
			}
			if cast[question] == nil {
				cast[question] = map[string]string{}
			}
			if cast[question][e.Actor] != "" {
				t.Fatal("duplicate vote by seat", e.Actor)
			}
			cast[question][e.Actor] = option
		case "vote.result":
			question := e.Str("question")
			if !e.Public || results[question] || phase != "vote" || !slices.ContainsFunc(p.Votes, func(q script.Vote) bool { return q.ID == question }) {
				t.Fatal("invalid vote result publication")
			}
			for _, q := range p.Votes {
				if len(cast[q.ID]) != len(p.Characters) {
					t.Fatal("vote result published before all seats finished")
				}
			}
			counts := map[string]int{}
			for _, ch := range p.Characters {
				counts[ch.ID] = 0
			}
			for _, option := range cast[question] {
				counts[option]++
			}
			var reported map[string]int
			b, _ := json.Marshal(e.Payload["counts"])
			if json.Unmarshal(b, &reported) != nil || !reflect.DeepEqual(reported, counts) {
				t.Fatal("incorrect recomputed vote tally")
			}
			best := 0
			winners := []string{}
			for option, count := range counts {
				if count > best {
					best, winners = count, []string{option}
				} else if count == best && count > 0 {
					winners = append(winners, option)
				}
			}
			winner := ""
			if len(winners) == 1 {
				winner = winners[0]
			}
			correct := false
			for _, q := range p.Votes {
				if q.ID == question {
					correct = q.Correct != "" && winner == q.Correct
				}
			}
			if value, ok := e.Payload["correct"].(bool); !ok || value != correct || e.Str("winner") != winner {
				t.Fatal("incorrect vote winner or correctness")
			}
			results[question] = true
		case "pm.send":
			to := e.Str("to")
			if e.Public || len(e.To) != 2 || to == e.Actor || !joined[to] || !joined[e.Actor] || !slices.Contains(e.To, to) || !slices.Contains(e.To, e.Actor) {
				t.Fatal("private message audience differs from sender/recipient")
			}
		case "script.section":
			if e.Public || len(e.To) != 1 || e.To[0] != e.Actor {
				t.Fatal("private script exposed")
			}
		case "ai.result":
			aiCalls++
			if expectedProvider != "" && e.Str("provider") != expectedProvider {
				t.Fatal("unexpected AI provider", e.Str("provider"), "want", expectedProvider)
			}
			if b, _ := e.Payload["fallback"].(bool); b {
				fallbacks++
			}
			if e.Public || len(e.To) != 1 || e.To[0] != e.Actor || kinds[e.Actor] != "ai" || e.Str("pid") != e.Actor {
				t.Fatal("private AI diagnostic exposed or wrong seat")
			}
		case "game.end":
			ended = true
		}
	}
	for _, e := range evs {
		if e.Type == "chat.say" || e.Type == "narration" || e.Type == "reveal" {
			if pairedTTS[e.Seq] != 1 {
				t.Fatal("source must have exactly one TTS event", e.Type, e.Seq, pairedTTS[e.Seq])
			}
		}
	}
	if len(results) != len(p.Votes) {
		t.Fatal("not all vote questions have results")
	}
	if expectedProvider == "qodercli" && unlocksFound == 0 {
		t.Fatal("genuine E2E did not exercise an unlocked clue")
	}
	want := []string{}
	for _, s := range p.Flow {
		want = append(want, s.ID)
	}
	if !reflect.DeepEqual(phases, want) {
		t.Fatalf("flow differs: got %v want %v", phases, want)
	}
	if !ended || len(joined) != 5 {
		t.Fatal("not a completed five-player game")
	}
	for pid := range expectedAI {
		if !joined[pid] || kinds[pid] != "ai" {
			t.Fatal("required AI seat missing", pid)
		}
	}
	for _, ch := range p.Characters {
		if sayCount[ch.ID] != 5 {
			t.Errorf("%s speech count %d want 5", ch.ID, sayCount[ch.ID])
		}
		if ttsCount[ch.ID] < sayCount[ch.ID] {
			t.Error("missing TTS")
		}
		for _, step := range p.Flow {
			if step.Type != "intro" && step.Type != "discussion" {
				continue
			}
			for round := 1; round <= step.Int("rounds", 1); round++ {
				if count := speechTurns[speechTurn{step.ID, round, ch.ID}]; count != 1 {
					t.Errorf("%s phase %s round %d speech count %d want 1", ch.ID, step.ID, round, count)
				}
			}
		}
	}
	if votes != 5 {
		t.Errorf("vote count %d want 5", votes)
	}
	if len(owners) == 0 {
		t.Fatal("game skipped investigation")
	}
	if expectedProvider != "" && aiCalls == 0 {
		t.Fatal("no AI results from required provider")
	}
	if aiCalls > 0 && float64(fallbacks)/float64(aiCalls) > 0.25 {
		t.Errorf("AI fallback rate %d/%d exceeds 25%%", fallbacks, aiCalls)
	}
	var result *engine.Event
	var reveal *engine.Event
	for i := range evs {
		e := &evs[i]
		if e.Type == "vote.result" {
			result = e
		}
		if e.Type == "reveal" {
			reveal = e
		}
	}
	if result == nil || reveal == nil {
		t.Fatal("missing final vote or reveal")
	}
	if reveal.Str("truth") != p.Truth {
		t.Fatal("incomplete truth revealed")
	}
	correct, _ := result.Payload["correct"].(bool)
	if correct && reveal.Str("title") != "真凶浮出水面" {
		t.Fatal("wrong ending for correct vote")
	}
	if !correct && reveal.Str("title") != "烟花掩盖的秘密" {
		t.Fatal("wrong ending for incorrect vote")
	}
	t.Logf("room=%s events=%d phases=%d speech=%v votes=%d ai=%d fallback=%d winner=%s", room, len(evs), len(phases), sayCount, votes, aiCalls, fallbacks, result.Str("winner"))
}
