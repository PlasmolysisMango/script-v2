package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"jbs/internal/engine"
	"jbs/internal/plugins"
	"jbs/internal/protocol"
)

func TestHTTPAndWebSocket(t *testing.T) {
	reg, err := plugins.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	app, err := New(reg, nil, "../../testdata/scripts", "")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	req := func(method, path, token string, v any) (int, []byte) {
		t.Helper()
		b, _ := json.Marshal(v)
		r, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, out
	}
	status, b := req("GET", "/api/scripts", "", nil)
	if status != 200 || strings.Contains(string(b), "私密经历") || strings.Contains(string(b), "correct") {
		t.Fatalf("script metadata privacy: %s", b)
	}
	status, b = req("POST", "/api/rooms", "", map[string]any{"script_id": "mini"})
	if status != 201 {
		t.Fatal(string(b))
	}
	var created map[string]string
	json.Unmarshal(b, &created)
	base := "/api/rooms/" + created["room_id"]
	status, _ = req("GET", base+"/view", "", nil)
	if status != 401 {
		t.Fatal("view should require auth")
	}
	tokens := map[string]string{}
	for _, pid := range []string{"a", "b", "c"} {
		status, b = req("POST", base+"/join", "", map[string]string{"char_id": pid, "name": pid})
		if status != 201 {
			t.Fatal(string(b))
		}
		var joined map[string]string
		json.Unmarshal(b, &joined)
		tokens[pid] = joined["token"]
	}
	status, _ = req("POST", base+"/join", "", map[string]string{"char_id": "a", "name": "duplicate"})
	if status != 409 {
		t.Fatal("duplicate join allowed")
	}
	_, b = req("GET", base+"/view", tokens["a"], nil)
	var v engine.View
	json.Unmarshal(b, &v)
	status, _ = req("POST", base+"/action", tokens["a"], map[string]any{"type": "ready", "actor": "b", "state_id": v.StateID})
	if status != 200 {
		t.Fatal("ready failed")
	}
	_, b = req("GET", base+"/view", tokens["a"], nil)
	json.Unmarshal(b, &v)
	for _, seat := range v.Seats {
		if seat.ID == "a" && !seat.Ready {
			t.Fatal("actor not overridden")
		}
		if seat.ID == "b" && seat.Ready {
			t.Fatal("actor spoof")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/rooms/"+created["room_id"]+"?token="+tokens["a"], nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var m struct {
			T    string      `json:"t"`
			View engine.View `json:"view"`
		}
		json.Unmarshal(data, &m)
		if m.T == "view" {
			if m.View.PID != "a" {
				t.Fatal("ws wrong view")
			}
			break
		}
	}
	conn2, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/rooms/"+created["room_id"]+"?token="+tokens["a"], &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{"https://foreign.invalid"}}})
	if conn2 != nil {
		conn2.CloseNow()
	}
	if err == nil || resp == nil || resp.StatusCode != 403 {
		t.Fatal("cross-origin ws accepted")
	}
}

func TestTwoWebSocketPlayersCompleteMiniGame(t *testing.T) {
	reg, err := plugins.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	app, err := New(reg, nil, "../../testdata/scripts", "")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()
	request := func(method, path, token string, body, result any) {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(method, ts.URL+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			t.Fatalf("request failed: %s", resp.Status)
		}
		if result != nil {
			if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
				t.Fatal(err)
			}
		}
	}
	var created map[string]string
	request("POST", "/api/rooms", "", map[string]string{"script_id": "mini"}, &created)
	base := "/api/rooms/" + created["room_id"]
	tokens := map[string]string{}
	for _, pid := range []string{"a", "b", "c"} {
		var joined map[string]string
		request("POST", base+"/join", "", map[string]string{"char_id": pid, "name": pid}, &joined)
		tokens[pid] = joined["token"]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connections := map[string]*websocket.Conn{}
	for _, pid := range []string{"a", "b"} {
		conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/rooms/"+created["room_id"], &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + tokens[pid]}}})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.CloseNow()
		connections[pid] = conn
	}
	seenEnd := map[string]bool{}
	read := func(pid string) protocol.Message {
		t.Helper()
		_, data, err := connections[pid].Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var msg protocol.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.View != nil && msg.View.PID != pid {
			t.Fatal("another player's view received")
		}
		if msg.View != nil && msg.View.Ended {
			seenEnd[pid] = true
		}
		if msg.Event != nil && !msg.Event.VisibleTo(pid) {
			t.Fatal("private event leaked across sockets")
		}
		return msg
	}
	ended := false
	for turn := 0; turn < 100 && !ended; turn++ {
		progress := false
		for _, pid := range []string{"a", "b", "c"} {
			var view engine.View
			request("GET", base+"/view", tokens[pid], nil, &view)
			if view.Ended {
				ended = true
				break
			}
			var selected *engine.ActionSpec
			for _, typ := range []string{"search", "say", "vote.cast", "ready"} {
				for i := range view.Allowed {
					if view.Allowed[i].Type == typ {
						selected = &view.Allowed[i]
						break
					}
				}
				if selected != nil {
					break
				}
			}
			if selected == nil {
				continue
			}
			var payload any
			switch selected.Type {
			case "search":
				payload = map[string]string{"location": selected.Options[0].Value}
			case "say":
				payload = map[string]string{"text": "请核对各位的时间线。"}
			case "vote.cast":
				payload = map[string]string{"question": "murderer", "option": "b"}
			}
			action := engine.NewAction(selected.Type, payload)
			action.StateID = view.StateID
			if conn := connections[pid]; conn != nil {
				id := fmt.Sprintf("%s-%d", pid, turn)
				data, err := json.Marshal(protocol.Message{Type: "action", ID: id, Action: &action})
				if err != nil {
					t.Fatal(err)
				}
				if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
					t.Fatal(err)
				}
				for {
					msg := read(pid)
					if msg.Type == "ack" && msg.ID == id {
						if !msg.OK {
							t.Fatalf("socket action rejected: %s", msg.Error)
						}
						break
					}
				}
			} else {
				request("POST", base+"/action", tokens[pid], action, nil)
			}
			progress = true
		}
		if !ended && !progress {
			t.Fatal("game made no progress")
		}
	}
	if !ended {
		t.Fatal("mini game did not complete")
	}
	for _, pid := range []string{"a", "b"} {
		for !seenEnd[pid] {
			read(pid)
		}
	}
}
