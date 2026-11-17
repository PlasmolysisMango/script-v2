package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"jbs/internal/engine"
	"jbs/internal/importer/mirage"
	"jbs/internal/protocol"
)

type Session struct {
	Server    string `json:"server"`
	Room      string `json:"room_id"`
	PID       string `json:"player_id"`
	Token     string `json:"token"`
	HostToken string `json:"host_token,omitempty"`
	LastSeq   int64  `json:"last_seq"`
}
type CLI struct {
	session Session
	path    string
	json    bool
	client  *http.Client
}

func main() { code := run(); os.Exit(code) }
func run() int {
	args := os.Args[1:]
	profile := "default"
	server := ""
	asJSON := false
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			asJSON = true
		case a == "--profile" || a == "--server":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "missing value for", a)
				return 1
			}
			i++
			if a == "--profile" {
				profile = args[i]
			} else {
				server = args[i]
			}
		case strings.HasPrefix(a, "--profile="):
			profile = strings.TrimPrefix(a, "--profile=")
		case strings.HasPrefix(a, "--server="):
			server = strings.TrimPrefix(a, "--server=")
		default:
			rest = append(rest, a)
		}
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,60}$`).MatchString(profile) {
		fmt.Fprintln(os.Stderr, "invalid profile name")
		return 1
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return report(err)
	}
	c := &CLI{path: filepath.Join(home, ".jbs", profile+".json"), json: asJSON, client: &http.Client{Timeout: 35 * time.Second}}
	if b, err := os.ReadFile(c.path); err == nil {
		if err = json.Unmarshal(b, &c.session); err != nil {
			return report(err)
		}
	}
	if server != "" {
		c.session.Server = server
	}
	if c.session.Server == "" {
		c.session.Server = "http://127.0.0.1:8080"
	}
	c.session.Server = strings.TrimSuffix(c.session.Server, "/")
	u, err := url.Parse(c.session.Server)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return report(errors.New("invalid server URL"))
	}
	if len(rest) == 0 || rest[0] == "help" || rest[0] == "--help" {
		fmt.Println("jbs [--profile NAME] [--server URL] [--json] room create|list | scripts | join ROOM --char ID --name NAME | state | action TYPE JSON | say TEXT | pass | ready | advance | search LOCATION | clues | publish ID | vote QUESTION OPTION | pm TARGET TEXT | wait --until-actions|--until-my-turn|--until-phase ID --timeout 10m | log --since N | import mirage DIR --truth FILE --culprit NAME --out DIR")
		return 0
	}
	code, err := c.command(rest)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		if code == 0 {
			return 1
		}
	}
	return code
}
func report(err error) int { fmt.Fprintln(os.Stderr, err); return 1 }
func (c *CLI) save() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c.session, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err = os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}
func (c *CLI) request(method, path string, body any, token string, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.session.Server+path, reader)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var v struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &v) == nil && v.Error != "" {
			return errors.New(v.Error)
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}
func (c *CLI) output(v any) { b, _ := json.MarshalIndent(v, "", "  "); fmt.Println(string(b)) }
func (c *CLI) roomPath(suffix string) string {
	return "/api/rooms/" + url.PathEscape(c.session.Room) + suffix
}
func (c *CLI) view() (engine.View, error) {
	var v engine.View
	if c.session.Room == "" || c.session.Token == "" {
		return v, errors.New("join a room first")
	}
	err := c.request("GET", c.roomPath("/view"), nil, c.session.Token, &v)
	return v, err
}
func flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}
func (c *CLI) command(args []string) (int, error) {
	cmd, tail := args[0], args[1:]
	switch cmd {
	case "scripts":
		var out any
		err := c.request("GET", "/api/scripts", nil, "", &out)
		if err == nil {
			c.output(out)
		}
		return 0, err
	case "import":
		if len(tail) < 2 || tail[0] != "mirage" {
			return 1, errors.New("usage: import mirage DIR --truth FILE --culprit NAME --out DIR")
		}
		fs := flags("import")
		truth := fs.String("truth", filepath.Join(filepath.Dir(tail[1]), "Truth.json"), "truth JSON")
		culprit := fs.String("culprit", "修经理", "culprit")
		out := fs.String("out", "scripts/eastern-star", "output directory")
		if err := fs.Parse(tail[2:]); err != nil {
			return 1, err
		}
		p, err := mirage.Import(tail[1], *truth, *culprit)
		if err != nil {
			return 1, err
		}
		if err = p.Save(*out); err != nil {
			return 1, err
		}
		c.output(map[string]any{"script_id": p.Meta.ID, "characters": len(p.Characters), "clues": len(p.Clues), "out": *out})
		return 0, nil
	case "room":
		if len(tail) == 0 {
			return 1, errors.New("room create|list")
		}
		if tail[0] == "list" {
			var out any
			err := c.request("GET", "/api/rooms", nil, "", &out)
			if err == nil {
				c.output(out)
			}
			return 0, err
		}
		if tail[0] != "create" {
			return 1, errors.New("room create|list")
		}
		fs := flags("create")
		id := fs.String("script", "eastern-star", "script id")
		seats := fs.String("ai-seats", "", "comma separated character ids")
		llm := fs.String("llm", "mock", "mock or qoder")
		if err := fs.Parse(tail[1:]); err != nil {
			return 1, err
		}
		ai := []string{}
		if *seats != "" {
			ai = strings.Split(*seats, ",")
		}
		var out struct {
			Room string `json:"room_id"`
			Host string `json:"host_token"`
		}
		err := c.request("POST", "/api/rooms", map[string]any{"script_id": *id, "ai_seats": ai, "llm": *llm}, "", &out)
		if err != nil {
			return 1, err
		}
		c.session.Room = out.Room
		c.session.Token = out.Host
		c.session.HostToken = out.Host
		c.session.PID = engine.HostID
		c.session.LastSeq = 0
		if err = c.save(); err != nil {
			return 1, err
		}
		c.output(out)
		return 0, nil
	case "join":
		if len(tail) == 0 {
			return 1, errors.New("join ROOM --char ID --name NAME")
		}
		fs := flags("join")
		ch := fs.String("char", "", "character id")
		name := fs.String("name", "CLI 玩家", "name")
		if err := fs.Parse(tail[1:]); err != nil {
			return 1, err
		}
		room := tail[0]
		var out struct {
			Room  string `json:"room_id"`
			PID   string `json:"player_id"`
			Token string `json:"token"`
		}
		err := c.request("POST", "/api/rooms/"+url.PathEscape(room)+"/join", map[string]string{"char_id": *ch, "name": *name}, "", &out)
		if err != nil {
			return 1, err
		}
		if room != c.session.Room {
			c.session.HostToken = ""
		}
		c.session.Room, c.session.PID, c.session.Token, c.session.LastSeq = out.Room, out.PID, out.Token, 0
		err = c.save()
		if err == nil {
			c.output(out)
		}
		return 0, err
	case "state":
		v, err := c.view()
		if err == nil {
			c.output(v)
		}
		return 0, err
	case "clues":
		v, err := c.view()
		if err == nil {
			c.output(v.Data["clues"])
		}
		return 0, err
	case "wait":
		return c.wait(tail)
	case "log":
		fs := flags("log")
		since := fs.Int64("since", 0, "sequence")
		if err := fs.Parse(tail); err != nil {
			return 1, err
		}
		var out []engine.Event
		err := c.request("GET", c.roomPath("/log")+"?since="+strconv.FormatInt(*since, 10), nil, c.session.Token, &out)
		if err == nil {
			c.output(out)
		}
		return 0, err
	case "ready", "pass", "advance", "say", "search", "publish", "vote", "pm", "action":
		typ := cmd
		var payload any
		switch cmd {
		case "advance":
			typ = "host.advance"
		case "say":
			if len(tail) == 0 {
				return 1, errors.New("say TEXT")
			}
			payload = map[string]string{"text": strings.Join(tail, " ")}
		case "search":
			if len(tail) != 1 {
				return 1, errors.New("search LOCATION")
			}
			payload = map[string]string{"location": tail[0]}
		case "publish":
			if len(tail) != 1 {
				return 1, errors.New("publish CLUE_ID")
			}
			payload = map[string]string{"clue": tail[0]}
		case "vote":
			if len(tail) != 2 {
				return 1, errors.New("vote QUESTION OPTION")
			}
			typ = "vote.cast"
			payload = map[string]string{"question": tail[0], "option": tail[1]}
		case "pm":
			if len(tail) < 2 {
				return 1, errors.New("pm TARGET TEXT")
			}
			typ = "pm.send"
			payload = map[string]string{"to": tail[0], "text": strings.Join(tail[1:], " ")}
		case "action":
			if len(tail) != 2 {
				return 1, errors.New("action TYPE JSON_PAYLOAD")
			}
			typ = tail[0]
			if err := json.Unmarshal([]byte(tail[1]), &payload); err != nil {
				return 1, err
			}
		}
		tok := c.session.Token
		if cmd == "advance" && c.session.HostToken != "" {
			tok = c.session.HostToken
		}
		var v engine.View
		for i := 0; i < 3; i++ {
			err := c.request("GET", c.roomPath("/view"), nil, tok, &v)
			if err != nil {
				return 1, err
			}
			a := engine.NewAction(typ, payload)
			a.StateID = v.StateID
			err = c.request("POST", c.roomPath("/action"), a, tok, &v)
			if err == nil {
				c.output(map[string]any{"ok": true, "phase": v.Phase, "ended": v.Ended, "state_id": v.StateID})
				return 0, nil
			}
			if !strings.Contains(err.Error(), "stale state_id") {
				return 1, err
			}
		}
		return 1, engine.ErrStale
	default:
		return 1, fmt.Errorf("unknown command %q", cmd)
	}
}
func (c *CLI) wait(args []string) (int, error) {
	fs := flags("wait")
	myturn := fs.Bool("until-my-turn", false, "wait for turn")
	actions := fs.Bool("until-actions", false, "wait for mandatory actions")
	phase := fs.String("until-phase", "", "phase id or type")
	dur := fs.Duration("timeout", 10*time.Minute, "timeout")
	if err := fs.Parse(args); err != nil {
		return 1, err
	}
	if c.session.Room == "" || c.session.Token == "" {
		return 1, errors.New("join a room first")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *dur)
	defer cancel()
	u, _ := url.Parse(c.session.Server)
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = "/ws/rooms/" + c.session.Room
	q := u.Query()
	q.Set("since", strconv.FormatInt(c.session.LastSeq, 10))
	u.RawQuery = q.Encode()
	conn, _, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + c.session.Token}}})
	if err != nil {
		if ctx.Err() != nil {
			return 2, errors.New("wait timed out")
		}
		return 1, err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(8 << 20)
	events := []engine.Event{}
	seen := make(map[int64]struct{})
	// Views may overlap replayed events; filter against the previous wait only.
	since := c.session.LastSeq
	cursor := since
	var latestView *engine.View
	emit := func(code int) (int, error) {
		out := map[string]any{"view": latestView, "events": events}
		if code == 3 {
			out["ended"] = true
		}
		c.output(out)
		c.session.LastSeq = cursor
		if err := c.save(); err != nil {
			return 1, err
		}
		if code == 2 {
			return 2, errors.New("wait timed out")
		}
		return code, nil
	}
	for {
		_, b, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return emit(2)
			}
			return 1, err
		}
		var msg protocol.Message
		if err = json.Unmarshal(b, &msg); err != nil {
			return 1, err
		}
		if msg.Event != nil {
			if _, duplicate := seen[msg.Event.Seq]; msg.Event.Seq > since && !duplicate {
				events = append(events, *msg.Event)
				seen[msg.Event.Seq] = struct{}{}
				cursor = max(cursor, msg.Event.Seq)
			}
			continue
		}
		if msg.View == nil {
			continue
		}
		v := *msg.View
		latestView = msg.View
		cursor = max(cursor, v.LastSeq)
		if v.Ended {
			return emit(3)
		}
		matched := *phase != "" && (v.Phase.ID == *phase || v.Phase.Type == *phase)
		for _, a := range v.Allowed {
			if !a.Optional && a.Type != "host.advance" {
				if *actions || (!*myturn && *phase == "") {
					matched = true
				}
				if *myturn && (a.Type == "say" || a.Type == "pass") {
					matched = true
				}
			}
		}
		if matched {
			return emit(0)
		}
	}
}
