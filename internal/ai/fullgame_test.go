package ai_test

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"jbs/internal/ai"
	"jbs/internal/ai/llm"
	"jbs/internal/engine"
	"jbs/internal/plugins"
	"jbs/internal/script"
)

type privacyRoom struct {
	*engine.Room
	pkg    *script.Package
	t      *testing.T
	mu     sync.Mutex
	phases map[string]bool
}

func (r *privacyRoom) View(pid string) engine.View {
	v := r.Room.View(pid)
	r.mu.Lock()
	r.phases[v.Phase.ID] = true
	r.mu.Unlock()
	if v.PID != pid || v.Me == nil || v.Me.ID != pid {
		r.t.Errorf("invalid own-character view for %s", pid)
	}
	data := map[string]any{}
	for key, value := range v.Data {
		if key != "reveal" || (v.Phase.Type != "reveal" && !v.Ended) {
			data[key] = value
		}
	}
	filtered := v
	filtered.Data = data
	b, _ := json.Marshal(filtered)
	for _, other := range r.pkg.Characters {
		if other.ID == pid {
			continue
		}
		for _, section := range other.Sections {
			quoted, _ := json.Marshal(section.Body)
			if section.Body != "" && bytes.Contains(b, quoted[1:len(quoted)-1]) {
				r.t.Errorf("%s sees %s private section at %s", pid, other.ID, v.Phase.ID)
			}
		}
	}
	visibleClues := map[string]bool{}
	ownVotes := map[string]string{}
	for _, e := range r.Log(pid, 0) {
		if e.Seq > v.LastSeq {
			continue
		}
		switch e.Type {
		case "clue.found", "clue.public", "clue.published":
			visibleClues[e.Str("id")] = true
		case "vote.cast":
			ownVotes[e.Str("question")] = e.Str("option")
		}
	}
	allowedBodies := map[string]bool{}
	for _, clue := range r.pkg.Clues {
		if visibleClues[clue.ID] {
			allowedBodies[clue.Body] = true
		}
	}
	clueData, _ := json.Marshal(data)
	for _, clue := range r.pkg.Clues {
		if clue.Visibility == "public" || visibleClues[clue.ID] || allowedBodies[clue.Body] || clue.Body == "" {
			continue
		}
		quoted, _ := json.Marshal(clue.Body)
		if bytes.Contains(clueData, quoted[1:len(quoted)-1]) {
			r.t.Errorf("%s sees a private undisclosed clue %s at %s", pid, clue.ID, v.Phase.ID)
		}
	}
	if votes, exists := v.Data["votes"]; exists {
		var actual struct {
			Cast map[string]string `json:"cast"`
		}
		encoded, _ := json.Marshal(votes)
		if json.Unmarshal(encoded, &actual) != nil || !reflect.DeepEqual(actual.Cast, ownVotes) {
			r.t.Errorf("%s vote view differs from own casts", pid)
		}
	}
	return v
}

func (r *privacyRoom) Log(pid string, since int64) []engine.Event {
	es := r.Room.Log(pid, since)
	for _, e := range es {
		if !e.VisibleTo(pid) {
			r.t.Errorf("event audience leaked to %s", pid)
		}
		switch e.Type {
		case "script.section", "clue.found", "vote.cast", "ai.result":
			if e.Public || e.Actor != pid || len(e.To) != 1 || e.To[0] != pid {
				r.t.Errorf("private %s leaked to %s", e.Type, pid)
			}
		}
	}
	return es
}

func TestSerializedMultilinePrivacyDetection(t *testing.T) {
	body := "私密第一行\n私密第二行\"引号\""
	encoded, _ := json.Marshal(map[string]string{"leak": body})
	quoted, _ := json.Marshal(body)
	if bytes.Contains(encoded, []byte(body)) || !bytes.Contains(encoded, quoted[1:len(quoted)-1]) {
		t.Fatal("escaped multiline secrets must be detected")
	}
}

func TestFullGameMock(t *testing.T) {
	for _, path := range []string{"../../testdata/scripts/mini", "../../scripts/eastern-star"} {
		t.Run(path, func(t *testing.T) {
			p, err := script.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			reg, err := plugins.NewRegistry()
			if err != nil {
				t.Fatal(err)
			}
			r, err := engine.NewRoom(engine.NewToken(), p, reg, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			checked := &privacyRoom{Room: r, pkg: p, t: t, phases: map[string]bool{}}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			for _, ch := range p.Characters {
				if _, err = r.Join(ch.ID, ch.Name, "ai"); err != nil {
					t.Fatal(err)
				}
			}
			stats := &ai.Stats{}
			for _, ch := range p.Characters {
				ai.Start(ctx, checked, ch.ID, llm.Mock{}, ai.Options{Stats: stats})
			}
			for {
				v := checked.View(p.Characters[0].ID)
				if v.Ended {
					break
				}
				if ctx.Err() != nil {
					t.Fatalf("mock game stalled at %s: stats=%+v", v.Phase.ID, stats.Snapshot())
				}
				r.Wait(ctx, v.LastSeq)
			}
			all := r.Log("*", 0)
			phases, says, tts, votes := 0, 0, 0, 0
			for _, e := range all {
				switch e.Type {
				case "phase.enter":
					phases++
				case "chat.say":
					says++
				case "tts":
					tts++
				case "vote.cast":
					votes++
				}
			}
			if phases != len(p.Flow) || says == 0 || tts < says || votes != len(p.Characters)*len(p.Votes) {
				t.Fatalf("bad full game: phases=%d say=%d tts=%d votes=%d", phases, says, tts, votes)
			}
			for _, ch := range p.Characters {
				checked.View(ch.ID)
				checked.Log(ch.ID, 0)
			}
			checked.mu.Lock()
			for _, step := range p.Flow {
				if step.Type != "reveal" && !checked.phases[step.ID] {
					t.Errorf("private views not inspected in phase %s", step.ID)
				}
			}
			checked.mu.Unlock()
			if stats.Snapshot().Fallbacks != 0 {
				t.Fatal("mock unexpectedly fell back", stats.Snapshot())
			}
			t.Logf("phases=%d speeches=%d tts=%d votes=%d", phases, says, tts, votes)
		})
	}
}
