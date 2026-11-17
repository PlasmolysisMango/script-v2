package store_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"jbs/internal/engine"
	"jbs/internal/script"
	"jbs/internal/store"
)

func roomID(t *testing.T) string {
	t.Helper()
	id, err := store.NewRoomID()
	if err != nil {
		t.Fatal(err)
	}
	if !store.ValidRoomID(id) {
		t.Fatal("generated invalid room ID")
	}
	return id
}

func newStore(t *testing.T, dir string) *store.JSONL {
	t.Helper()
	s, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func readEvents(t *testing.T, path string) []engine.Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	}()
	var events []engine.Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e engine.Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

func TestJSONLAppendAndPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "events")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := newStore(t, dir)
	id, second := roomID(t), roomID(t)
	if id == second || !store.ValidRoomID(engine.NewToken()) {
		t.Fatal("room IDs not unique or engine IDs rejected")
	}
	roomDir := filepath.Join(dir, id)
	if err := os.Mkdir(roomDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(roomDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(roomDir, "events.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	for seq := int64(1); seq <= 2; seq++ {
		e := engine.Event{Seq: seq, At: at, Type: "chat.say", Actor: "a", To: []string{"a", "b"}, Payload: map[string]any{"text": "Unicode 界\nand newline"}}
		if err := s.Append(id, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Append(second, engine.Event{Seq: 1, Type: "room.created", Public: true}); err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]os.FileMode{dir: 0o700, roomDir: 0o700, filepath.Join(dir, second): 0o700, path: 0o600, filepath.Join(dir, second, "events.jsonl"): 0o600} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != mode {
			t.Fatalf("%s mode: %o, want %o", path, fi.Mode().Perm(), mode)
		}
	}
	events := readEvents(t, path)
	if len(events) != 2 || events[0].Seq != 1 || events[1].Seq != 2 || !events[0].At.Equal(at) || events[0].Str("text") != "Unicode 界\nand newline" || events[0].Public {
		t.Fatalf("incorrect JSONL round trip: %+v", events)
	}
	if len(readEvents(t, filepath.Join(dir, second, "events.jsonl"))) != 1 || s.Err() != nil {
		t.Fatal("room logs mixed or unexpected stored error")
	}
}

func TestRejectUnsafeRoomIDs(t *testing.T) {
	dir := t.TempDir()
	s := newStore(t, dir)
	id := roomID(t)
	for _, unsafe := range []string{"", "room", "..", "../" + id, "/tmp/" + id, id + "/child", id + ".jsonl", strings.Repeat("z", 32), strings.Repeat("A", 32), id[:31], id + "0", " " + id, id + "\x00"} {
		if store.ValidRoomID(unsafe) {
			t.Fatalf("unsafe ID validated: %q", unsafe)
		}
		if err := s.Append(unsafe, engine.Event{}); err == nil {
			t.Fatalf("unsafe ID accepted: %q", unsafe)
		}
		if err := s.Snapshot(unsafe, map[string]any{}); err == nil {
			t.Fatalf("unsafe snapshot ID accepted: %q", unsafe)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 || s.Err() == nil {
		t.Fatal("unsafe append created files or errors lost")
	}
}

func TestRejectSymlinksHardlinksAndSpecialFiles(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "events")
	s := newStore(t, dir)
	outside := filepath.Join(base, "outside")
	if err := os.WriteFile(outside, []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(outside, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"events.jsonl", "snapshot.json"} {
		for _, kind := range []string{"symlink", "hardlink", "directory", "fifo"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				id := roomID(t)
				if err := os.Mkdir(filepath.Join(dir, id), 0o700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, id, name)
				var err error
				switch kind {
				case "symlink":
					err = os.Symlink(outside, path)
				case "hardlink":
					err = os.Link(outside, path)
				case "directory":
					err = os.Mkdir(path, 0o700)
				case "fifo":
					err = syscall.Mkfifo(path, 0o600)
				}
				if err != nil {
					t.Fatal(err)
				}
				if name == "events.jsonl" {
					err = s.Append(id, engine.Event{Type: "test"})
				} else {
					err = s.Snapshot(id, map[string]any{"test": true})
					entries, readErr := os.ReadDir(filepath.Join(dir, id))
					if readErr != nil || len(entries) != 1 {
						t.Fatalf("snapshot tempfile not cleaned up: %v, %v", entries, readErr)
					}
				}
				if err == nil {
					t.Fatalf("%s %s accepted", kind, name)
				}
			})
		}
	}
	b, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "untouched" || fi.Mode().Perm() != 0o644 || s.Err() == nil {
		t.Fatal("outside file modified or errors lost")
	}
	linkDir := filepath.Join(base, "linked-events")
	if err := os.Symlink(dir, linkDir); err != nil {
		t.Fatal(err)
	}
	if _, err := store.New(linkDir); err == nil {
		t.Fatal("symlink store directory accepted")
	}
}

func TestErrorsAndCloseAreObservable(t *testing.T) {
	if _, err := store.New(""); err == nil {
		t.Fatal("empty store directory accepted")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.New(file); err == nil {
		t.Fatal("file store directory accepted")
	}
	s := newStore(t, filepath.Join(dir, "events"))
	id := roomID(t)
	if err := s.Append(id, engine.Event{Payload: map[string]any{"bad": func() {}}}); err == nil || s.Err() == nil {
		t.Fatal("encoding failure was lost")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(id, engine.Event{}); err == nil || !strings.Contains(s.Err().Error(), "closed") {
		t.Fatal("closed store error was lost")
	}
	if err := s.Snapshot(id, map[string]any{}); err == nil || !strings.Contains(s.Err().Error(), "closed") {
		t.Fatal("closed snapshot error was lost")
	}
	if err := s.Close(); err != nil {
		t.Fatal("Close must be idempotent")
	}
}

func TestSnapshotAtomicAndPermissions(t *testing.T) {
	dir := t.TempDir()
	s := newStore(t, dir)
	id := roomID(t)
	path := filepath.Join(dir, id, "snapshot.json")
	value := map[string]any{"last_seq": 1, "ext": strings.Repeat("private 界", 1000)}
	if err := s.Snapshot(id, value); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	oldInfo, err := old.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	value["last_seq"] = 2
	if err := s.Snapshot(id, value); err != nil {
		t.Fatal(err)
	}
	newInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(oldInfo, newInfo) {
		t.Fatal("snapshot was updated in place instead of replaced atomically")
	}
	oldBytes, err := io.ReadAll(old)
	if err != nil || !bytes.Equal(oldBytes, original) {
		t.Fatal("replacement changed the previously open snapshot")
	}
	for path, mode := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, id): 0o700, path: 0o600} {
		fi, err := os.Stat(path)
		if err != nil || fi.Mode().Perm() != mode {
			t.Fatalf("incorrect persistence permissions for %s: %v, %v", path, fi, err)
		}
	}
	latest, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(latest, &decoded); err != nil || decoded["last_seq"] != float64(2) || decoded["ext"] != value["ext"] {
		t.Fatalf("snapshot round trip: %v, %v", decoded, err)
	}
	if err := s.Snapshot(id, map[string]any{"bad": func() {}}); err == nil || s.Err() == nil {
		t.Fatal("snapshot encoding failure was lost")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, latest) {
		t.Fatal("failed snapshot replaced the previous data")
	}

	// Readers must see only complete old or new JSON during repeated replacements.
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			b, err := os.ReadFile(path)
			var v map[string]any
			if err != nil || json.Unmarshal(b, &v) != nil || v["ext"] != value["ext"] {
				t.Error("reader observed missing, partial, or corrupt snapshot")
				return
			}
		}
	})
	for seq := 3; seq <= 15; seq++ {
		if err := s.Snapshot(id, map[string]any{"last_seq": seq, "ext": value["ext"]}); err != nil {
			t.Error(err)
			break
		}
	}
	close(done)
	wg.Wait()
	entries, err := os.ReadDir(filepath.Join(dir, id))
	if err != nil || len(entries) != 1 || entries[0].Name() != "snapshot.json" {
		t.Fatalf("unexpected snapshot artifacts: %v, %v", entries, err)
	}
}

func TestRejectUnsafeRoomDirectories(t *testing.T) {
	dir := t.TempDir()
	s := newStore(t, dir)
	outside := t.TempDir()
	inside := filepath.Join(dir, roomID(t))
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{outside, filepath.Base(inside)} {
		id := roomID(t)
		if err := os.Symlink(target, filepath.Join(dir, id)); err != nil {
			t.Fatal(err)
		}
		if err := s.Append(id, engine.Event{}); err == nil {
			t.Fatal("symlink room directory accepted by Append")
		}
		if err := s.Snapshot(id, map[string]any{}); err == nil {
			t.Fatal("symlink room directory accepted by Snapshot")
		}
	}
	for _, path := range []string{outside, inside} {
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 0 {
			t.Fatal("unsafe room directory received persistence data")
		}
	}
}

func TestConcurrentJSONLAppends(t *testing.T) {
	dir := t.TempDir()
	s := newStore(t, dir)
	id := roomID(t)
	const count = 30
	var wg sync.WaitGroup
	for i := 1; i <= count; i++ {
		wg.Go(func() {
			if err := s.Append(id, engine.Event{Seq: int64(i), Type: "test", Payload: map[string]any{"text": strings.Repeat("界", 4000)}}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	events := readEvents(t, filepath.Join(dir, id, "events.jsonl"))
	seen := map[int64]bool{}
	for _, e := range events {
		if seen[e.Seq] || e.Type != "test" || e.Str("text") != strings.Repeat("界", 4000) {
			t.Fatal("duplicate or interleaved events")
		}
		seen[e.Seq] = true
	}
	if len(seen) != count || s.Err() != nil {
		t.Fatal("lost concurrent appends")
	}
}

func persistenceRoom(t *testing.T, s engine.Store) *engine.Room {
	t.Helper()
	reg, err := engine.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	reg.PhaseType("test", engine.PhaseType{
		OnEnter: func(c *engine.Ctx) {
			c.Set("private", "inspection-only")
			c.State().Deadline = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
		},
		Allowed: func(c *engine.Ctx, pid string) []engine.ActionSpec {
			return []engine.ActionSpec{{Type: "finish"}}
		},
	})
	reg.Action("finish", func(c *engine.Ctx, a engine.Action) error { c.Advance(); return nil })
	reg.Hook(engine.Hooks{OnGameEnd: func(c *engine.Ctx) { c.Set("finished", true) }})
	pkg := &script.Package{
		Meta:       script.Meta{ID: "persist-test", Title: "Persistence", Players: 1},
		Characters: []script.Character{{ID: "a", Name: "A"}},
		Flow:       []script.Step{{ID: "first", Type: "test"}, {ID: "end", Type: "end"}},
	}
	r, err := engine.NewRoom(roomID(t), pkg, reg, s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r
}

func TestRoomInspectionSnapshot(t *testing.T) {
	dir := t.TempDir()
	s := newStore(t, dir)
	r := persistenceRoom(t, s)
	path := filepath.Join(dir, r.ID, "snapshot.json")
	token, err := r.Join("a", "Alice", "human")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snap struct {
		RoomID          string          `json:"room_id"`
		ScriptID        string          `json:"script_id"`
		PhaseIndex      int             `json:"phase_index"`
		StateID         int64           `json:"state_id"`
		PhaseGeneration int64           `json:"phase_generation"`
		Seats           []engine.Seat   `json:"seats"`
		Ended           bool            `json:"ended"`
		Visited         map[string]bool `json:"visited"`
		Deadline        time.Time       `json:"deadline"`
		Ext             map[string]any  `json:"ext"`
		LastSeq         int64           `json:"last_seq"`
	}
	if err := json.Unmarshal(b, &snap); err != nil {
		t.Fatal(err)
	}
	v := r.View("a")
	if snap.RoomID != r.ID || snap.ScriptID != "persist-test" || snap.PhaseIndex != 0 || snap.StateID != v.StateID || snap.PhaseGeneration != 1 || snap.LastSeq != r.LastSeq() || snap.Ended || !snap.Visited["first"] || !snap.Deadline.Equal(v.Phase.Deadline) || snap.Ext["private"] != "inspection-only" {
		t.Fatalf("incorrect inspection state: %+v", snap)
	}
	if len(snap.Seats) != 1 || !snap.Seats[0].Joined || snap.Seats[0].Name != "Alice" || snap.Seats[0].Token != "" || bytes.Contains(b, []byte(token)) || bytes.Contains(b, []byte(r.HostToken)) || bytes.Contains(b, []byte("token")) {
		t.Fatal("snapshot seats incorrect or credentials persisted")
	}
	public, err := json.Marshal(v)
	if err != nil || bytes.Contains(public, []byte("inspection-only")) {
		t.Fatal("private inspection data leaked into public view")
	}
	if err := r.Submit(engine.Action{Type: "finish", Actor: "a"}); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &snap); err != nil {
		t.Fatal(err)
	}
	if !snap.Ended || snap.PhaseIndex != 1 || snap.PhaseGeneration != 2 || !snap.Visited["end"] || snap.Ext["finished"] != true || !snap.Deadline.IsZero() || snap.LastSeq != r.LastSeq() || snap.StateID != r.View("a").StateID {
		t.Fatalf("snapshot did not capture settled actor state: %+v", snap)
	}
	events := readEvents(t, filepath.Join(dir, r.ID, "events.jsonl"))
	if events[len(events)-1].Seq != snap.LastSeq || r.PersistError() != nil || s.Err() != nil {
		t.Fatal("snapshot sequence or persistence error incorrect")
	}
}

type appendOnlyStore struct{ err error }

func (s appendOnlyStore) Append(string, engine.Event) error { return s.err }

type failingSnapshotStore struct {
	appendOnlyStore
	snapshotErr error
}

func (s failingSnapshotStore) Snapshot(string, any) error { return s.snapshotErr }

func TestRoomPersistenceFailures(t *testing.T) {
	appendErr, snapshotErr := errors.New("append unavailable"), errors.New("snapshot unavailable")
	for _, tc := range []struct {
		name  string
		store engine.Store
		want  []error
	}{
		{"append-only", appendOnlyStore{}, nil},
		{"append", appendOnlyStore{appendErr}, []error{appendErr}},
		{"snapshot", failingSnapshotStore{snapshotErr: snapshotErr}, []error{snapshotErr}},
		{"both", failingSnapshotStore{appendOnlyStore{appendErr}, snapshotErr}, []error{appendErr, snapshotErr}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := persistenceRoom(t, tc.store)
			var wg sync.WaitGroup
			wg.Go(func() {
				for range 30 {
					_ = r.PersistError()
				}
			})
			for range 3 {
				r.Record("a", "test", nil)
			}
			wg.Wait()
			r.Close()
			err := r.PersistError()
			if len(tc.want) == 0 && err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !errors.Is(err, want) {
					t.Fatalf("persistence failure lost after Close: %v", err)
				}
			}
			public, marshalErr := json.Marshal(r.Log("a", 0))
			if marshalErr != nil || bytes.Contains(public, []byte("unavailable")) {
				t.Fatal("persistence failures leaked into events")
			}
		})
	}
}
