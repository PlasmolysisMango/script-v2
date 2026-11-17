package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"jbs/internal/ai"
	"jbs/internal/ai/llm"
	"jbs/internal/engine"
	"jbs/internal/plugins"
	"jbs/internal/script"
)

// TestImportedMockAI is a deterministic regression for the imported scripts:
// every seat is driven by the Mock LLM and must finish without fallbacks.
func TestImportedMockAI(t *testing.T) {
	root := os.Getenv("JBS_IMPORT_ROOT")
	if root == "" {
		t.Skip("set JBS_IMPORT_ROOT to the imported scripts directory")
	}
	for _, id := range []string{"k1", "k12"} {
		id := id
		t.Run(id, func(t *testing.T) {
			pkg, err := script.Load(filepath.Join(root, id, "script.json"))
			if err != nil {
				t.Fatal(err)
			}
			reg, err := plugins.NewRegistry()
			if err != nil {
				t.Fatal(err)
			}
			room, err := engine.NewRoom("mock-"+id, pkg, reg, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer room.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			seats := make([]string, 0, len(pkg.Characters))
			for _, ch := range pkg.Characters {
				seats = append(seats, ch.ID)
				if _, err = room.Join(ch.ID, ch.Name, "ai"); err != nil {
					t.Fatal(err)
				}
			}
			stats := &ai.Stats{}
			for _, pid := range seats {
				ai.Start(ctx, room, pid, llm.Mock{}, ai.Options{Stats: stats})
			}
			go advanceOpenings(ctx, room)
			lastPhase := ""
			for {
				v := room.View(seats[0])
				if v.Ended {
					break
				}
				if ctx.Err() != nil {
					t.Fatalf("mock game stalled at %s: %+v", v.Phase.ID, stats.Snapshot())
				}
				if v.Phase.ID != lastPhase {
					lastPhase = v.Phase.ID
					for _, pid := range seats {
						checkViewPrivacy(t, pkg, pid, room.View(pid))
					}
				}
				room.Wait(ctx, v.LastSeq)
			}
			if s := stats.Snapshot(); s.Fallbacks != 0 {
				t.Fatalf("mock fell back %d times: %+v", s.Fallbacks, s)
			}
			verifyRun(t, pkg, room, seats, false)
			verifyPrivacy(t, pkg, room, seats)
		})
	}
}

// advanceOpenings stands in for a human host: opening phases have no EndIf, so
// the host advances once every seat has marked ready.
func advanceOpenings(ctx context.Context, room *engine.Room) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		v := room.View(engine.HostID)
		if v.Ended || v.Phase.Type != "opening" || len(v.Seats) == 0 {
			continue
		}
		allReady := true
		for _, seat := range v.Seats {
			if !seat.Ready {
				allReady = false
			}
		}
		spec, ok := findAction(v.Allowed, "host.advance")
		if !allReady || !ok {
			continue
		}
		a := engine.Action{Type: spec.Type, Actor: engine.HostID, StateID: v.StateID, Payload: json.RawMessage(`{}`)}
		_ = room.Submit(a)
	}
}
