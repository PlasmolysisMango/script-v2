package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"jbs/internal/engine"
	"jbs/internal/protocol"
)

func TestClientAuthAndHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing auth")
		}
		if r.URL.Path == "/bad" {
			w.WriteHeader(409)
			w.Write([]byte(`{"error":"stale state_id"}`))
			return
		}
		json.NewEncoder(w).Encode(engine.View{RoomID: "r", PID: "a", StateID: 4})
	}))
	defer ts.Close()
	c := &CLI{session: Session{Server: ts.URL, Room: "r", PID: "a", Token: "secret"}, path: filepath.Join(t.TempDir(), "profile.json"), client: ts.Client()}
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	v, err := c.view()
	if err != nil || v.StateID != 4 {
		t.Fatal(v, err)
	}
	if err = c.request("GET", "/bad", nil, "secret", nil); err == nil || err.Error() != "stale state_id" {
		t.Fatal("wrong error", err)
	}
}

// Drain both pipes concurrently: a complete event batch can exceed pipe capacity.
func runCaptured(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	capture := func() (*os.File, <-chan string) {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan string, 1)
		go func() {
			b, err := io.ReadAll(r)
			if err != nil {
				t.Error(err)
			}
			r.Close()
			done <- string(b)
		}()
		return w, done
	}
	stdout, outDone := capture()
	stderr, errDone := capture()
	originalArgs, originalOut, originalErr := os.Args, os.Stdout, os.Stderr
	defer func() {
		os.Args, os.Stdout, os.Stderr = originalArgs, originalOut, originalErr
		stdout.Close()
		stderr.Close()
	}()
	os.Args, os.Stdout, os.Stderr = append([]string{"jbs"}, args...), stdout, stderr
	code := run()
	stdout.Close()
	stderr.Close()
	return code, <-outDone, <-errDone
}

func TestWaitDeliversAllEventsAndResumes(t *testing.T) {
	const start int64 = 7
	const count = 160
	const last = start + count
	const token = "private-player-token"
	const hostToken = "private-host-token"
	for _, tc := range []struct {
		name   string
		code   int
		noView bool
		ahead  bool
	}{
		{name: "matched", code: 0},
		{name: "ended", code: 3},
		{name: "timeout", code: 2},
		{name: "timeout-without-view", code: 2, noView: true},
		{name: "timeout-view-ahead", code: 2, ahead: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cursor := last
			viewSeq := last - 1 // A stale view must not roll back event progress.
			if tc.ahead {
				viewSeq = last + 3 // Views can advance past invisible events.
				cursor = viewSeq
			}
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/ws/rooms/r" || r.Header.Get("Authorization") != "Bearer "+token {
					t.Error("incorrect path or missing auth")
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				since, err := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
				if err != nil || (since != start && since != cursor) {
					t.Errorf("since=%d, want %d or %d; error=%v", since, start, cursor, err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.CloseNow()
				ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
				defer cancel()
				write := func(msg protocol.Message) bool {
					b, err := json.Marshal(msg)
					if err == nil {
						err = conn.Write(ctx, websocket.MessageText, b)
					}
					if err != nil {
						t.Error(err)
						return false
					}
					return true
				}
				event := func(seq int64) bool {
					return write(protocol.Message{Type: "event", Event: &engine.Event{
						Seq: seq, Type: "say", Public: true,
						Payload: map[string]any{"text": strings.Repeat("event ", 100)},
					}})
				}
				if since == start {
					// Overlapping views and out-of-order duplicates must not drop events.
					if !tc.noView && !write(protocol.Message{Type: "view", View: &engine.View{LastSeq: start + 10, StateID: 1}}) {
						return
					}
					if !event(start) || !event(start+2) {
						return
					}
					for seq := start + 1; seq <= last; seq++ {
						if !event(seq) || (seq%10 == 0 && !event(seq)) {
							return
						}
					}
					if !tc.noView {
						v := engine.View{RoomID: "r", PID: "a", LastSeq: viewSeq, StateID: 2, Ended: tc.code == 3}
						if tc.code == 0 {
							v.Allowed = []engine.ActionSpec{{Type: "ready"}}
						}
						if !write(protocol.Message{Type: "view", View: &v}) {
							return
						}
					}
				} else {
					// Replay the log using the persisted cursor, not process memory.
					for seq := start + 1; seq <= cursor+3; seq++ {
						if seq > since && !event(seq) {
							return
						}
					}
					v := engine.View{LastSeq: cursor + 3, Allowed: []engine.ActionSpec{{Type: "ready"}}}
					if !write(protocol.Message{Type: "view", View: &v}) {
						return
					}
				}
				_, _, _ = conn.Read(ctx)
			}))
			defer ts.Close()
			home := t.TempDir()
			t.Setenv("HOME", home)
			c := &CLI{session: Session{Server: ts.URL, Room: "r", PID: "a", Token: token, HostToken: hostToken, LastSeq: start}, path: filepath.Join(home, ".jbs", "wait-test.json")}
			if err := c.save(); err != nil {
				t.Fatal(err)
			}
			other := &CLI{session: Session{Room: "other", LastSeq: 999}, path: filepath.Join(home, ".jbs", "other.json")}
			if err := other.save(); err != nil {
				t.Fatal(err)
			}
			otherBefore, err := os.ReadFile(other.path)
			if err != nil {
				t.Fatal(err)
			}
			checkOutput := func(stdout, stderr string, first int64, n int, wantView bool, ended bool) *engine.View {
				t.Helper()
				if strings.Contains(stdout+stderr, token) || strings.Contains(stdout+stderr, hostToken) {
					t.Fatal("credentials leaked in CLI output")
				}
				var out struct {
					View   *engine.View   `json:"view"`
					Events []engine.Event `json:"events"`
					Ended  bool           `json:"ended"`
				}
				if err := json.Unmarshal([]byte(stdout), &out); err != nil {
					t.Fatalf("invalid output: %v", err)
				}
				var keys map[string]json.RawMessage
				if err := json.Unmarshal([]byte(stdout), &keys); err != nil {
					t.Fatal(err)
				}
				wantKeys := 2
				if ended {
					wantKeys++
				}
				if len(keys) != wantKeys || keys["view"] == nil || keys["events"] == nil || out.Ended != ended || (out.View != nil) != wantView {
					t.Fatal("unexpected wait output shape")
				}
				if len(out.Events) != n {
					t.Fatalf("received %d events, want %d", len(out.Events), n)
				}
				seen := make(map[int64]bool)
				for _, e := range out.Events {
					if e.Seq < first || e.Seq >= first+int64(n) || seen[e.Seq] {
						t.Fatalf("unexpected or duplicate event sequence %d", e.Seq)
					}
					seen[e.Seq] = true
				}
				return out.View
			}
			checkSession := func(wantSeq int64) {
				t.Helper()
				b, err := os.ReadFile(c.path)
				if err != nil {
					t.Fatal(err)
				}
				var saved Session
				if err := json.Unmarshal(b, &saved); err != nil {
					t.Fatal(err)
				}
				want := c.session
				want.LastSeq = wantSeq
				if saved != want {
					t.Fatalf("session changed unexpectedly; cursor=%d, want %d", saved.LastSeq, wantSeq)
				}
				otherAfter, err := os.ReadFile(other.path)
				if err != nil || string(otherAfter) != string(otherBefore) {
					t.Fatal("another profile was modified")
				}
			}
			duration := "3s"
			if tc.code == 2 {
				duration = "1s"
			}
			code, stdout, stderr := runCaptured(t, "--json", "--profile", "wait-test", "wait", "--until-actions", "--timeout", duration)
			if code != tc.code {
				t.Fatalf("exit=%d, want %d; stderr=%s", code, tc.code, stderr)
			}
			if tc.code == 2 && !strings.Contains(stderr, "wait timed out") {
				t.Fatal("missing documented timeout error")
			}
			v := checkOutput(stdout, stderr, start+1, count, !tc.noView, tc.code == 3)
			if v != nil && (v.StateID != 2 || v.LastSeq != viewSeq) {
				t.Fatal("did not emit the latest available view")
			}
			checkSession(cursor)
			code, stdout, stderr = runCaptured(t, "--json", "--profile", "wait-test", "wait", "--until-actions", "--timeout", "3s")
			if code != 0 {
				t.Fatalf("resumed exit=%d; stderr=%s", code, stderr)
			}
			checkOutput(stdout, stderr, cursor+1, 3, true, false)
			checkSession(cursor + 3)
		})
	}
}

func TestWaitExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name string
		view engine.View
		want int
	}{
		{"actions", engine.View{Allowed: []engine.ActionSpec{{Type: "ready"}}}, 0},
		{"timeout", engine.View{}, 2},
		{"ended", engine.View{Ended: true}, 3},
		{"unauthorized", engine.View{}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer secret" || tc.name == "unauthorized" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.CloseNow()
				data, err := json.Marshal(protocol.Message{Type: "view", View: &tc.view})
				if err != nil {
					t.Error(err)
					return
				}
				if err := conn.Write(r.Context(), websocket.MessageText, data); err != nil {
					t.Error(err)
					return
				}
				_, _, _ = conn.Read(context.Background())
			}))
			defer ts.Close()
			home := t.TempDir()
			t.Setenv("HOME", home)
			c := &CLI{session: Session{Server: ts.URL, Room: "r", PID: "a", Token: "secret"}, path: filepath.Join(home, ".jbs", "wait-test.json")}
			if err := c.save(); err != nil {
				t.Fatal(err)
			}
			original := os.Args
			duration := "1s"
			if tc.name == "timeout" {
				duration = "100ms"
			}
			os.Args = []string{"jbs", "--profile", "wait-test", "wait", "--until-actions", "--timeout", duration}
			defer func() { os.Args = original }()
			if code := run(); code != tc.want {
				t.Fatalf("exit=%d, want %d", code, tc.want)
			}
		})
	}
}
