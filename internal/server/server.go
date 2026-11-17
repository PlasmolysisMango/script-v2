package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"jbs/internal/engine"
	"jbs/internal/script"
)

type AIStarter func(ctx context.Context, room *engine.Room, pid, provider string)
type Options struct {
	AssetsDir string
	Strict    bool
}

type Server struct {
	registry   *engine.Registry
	scripts    map[string]*script.Package
	rooms      map[string]*engine.Room
	mu         sync.RWMutex
	store      engine.Store
	static     string
	assetDir   string
	ctx        context.Context
	cancel     context.CancelFunc
	StartAI    AIStarter
	DefaultLLM string
}

func New(reg *engine.Registry, store engine.Store, scriptsDir, static string, options ...Options) (*Server, error) {
	ctx, cancel := context.WithCancel(context.Background())
	var opts Options
	if len(options) > 0 {
		opts = options[0]
	}
	assetsDir := opts.AssetsDir
	if assetsDir == "" {
		assetsDir = filepath.Join(scriptsDir, "imported", "assets")
		if filepath.Base(filepath.Clean(scriptsDir)) == "imported" {
			assetsDir = filepath.Join(scriptsDir, "assets")
		}
	}
	s := &Server{registry: reg, store: store, static: static, assetDir: assetsDir,
		scripts: map[string]*script.Package{}, rooms: map[string]*engine.Room{}, ctx: ctx, cancel: cancel, DefaultLLM: "mock"}
	var paths []string
	err := filepath.WalkDir(scriptsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "assets" || d.Name() == "node_modules" || d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "script.json" {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		cancel()
		return nil, err
	}
	for _, path := range paths {
		p, err := script.Load(path)
		if err != nil {
			if opts.Strict {
				cancel()
				return nil, fmt.Errorf("load %s: %w", path, err)
			}
			log.Printf("skip script %s: %v", path, err)
			continue
		}
		if err = reg.CheckScript(p); err != nil {
			if opts.Strict {
				cancel()
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			log.Printf("skip script %s: %v", path, err)
			continue
		}
		if _, exists := s.scripts[p.Meta.ID]; exists {
			log.Printf("duplicate script id %s at %s (ignored)", p.Meta.ID, path)
			continue
		}
		s.scripts[p.Meta.ID] = p
	}
	return s, nil
}
func (s *Server) Close() {
	s.cancel()
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.rooms {
		r.Close()
	}
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"ok": true, "plugins": s.registry.Plugins()})
	})
	mux.HandleFunc("GET /api/scripts", s.listScripts)
	mux.HandleFunc("GET /api/rooms", s.listRooms)
	mux.HandleFunc("POST /api/rooms", s.createRoom)
	mux.HandleFunc("GET /api/rooms/{room}", s.roomInfo)
	mux.HandleFunc("POST /api/rooms/{room}/join", s.join)
	mux.HandleFunc("GET /api/rooms/{room}/view", s.view)
	mux.HandleFunc("GET /api/rooms/{room}/log", s.events)
	mux.HandleFunc("POST /api/rooms/{room}/action", s.action)
	mux.HandleFunc("GET /ws/rooms/{room}", s.ws)
	mux.HandleFunc("GET /script-assets/", s.scriptAsset)
	mux.HandleFunc("/", s.assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		mux.ServeHTTP(w, r)
	})
}
func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, err error) {
	reply(w, status, map[string]string{"error": err.Error()})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	defer r.Body.Close()
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		fail(w, 400, err)
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		fail(w, 400, errors.New("one JSON object required"))
		return false
	}
	return true
}
func (s *Server) room(w http.ResponseWriter, r *http.Request) *engine.Room {
	s.mu.RLock()
	rm := s.rooms[r.PathValue("room")]
	s.mu.RUnlock()
	if rm == nil {
		fail(w, 404, errors.New("room not found"))
	}
	return rm
}
func (s *Server) auth(w http.ResponseWriter, r *http.Request, rm *engine.Room, ws bool) (string, bool) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if ws && tok == "" {
		tok = r.URL.Query().Get("token")
	}
	pid, ok := rm.Token(tok)
	if !ok {
		fail(w, 401, errors.New("invalid room token"))
	}
	return pid, ok
}
func (s *Server) listScripts(w http.ResponseWriter, r *http.Request) {
	type pubChar struct {
		ID    string       `json:"id"`
		Name  string       `json:"name"`
		Intro string       `json:"public_intro"`
		TTS   script.Voice `json:"tts"`
	}
	type pubScript struct {
		Meta       script.Meta `json:"meta"`
		Characters []pubChar   `json:"characters"`
	}
	out := []pubScript{}
	for _, p := range s.scripts {
		ps := pubScript{Meta: p.Meta}
		for _, c := range p.Characters {
			ps.Characters = append(ps.Characters, pubChar{c.ID, c.Name, c.PublicIntro, c.TTS})
		}
		out = append(out, ps)
	}
	slices.SortFunc(out, func(a, b pubScript) int { return strings.Compare(a.Meta.ID, b.Meta.ID) })
	reply(w, 200, out)
}
func (s *Server) listRooms(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	rooms := make([]*engine.Room, 0, len(s.rooms))
	for _, rm := range s.rooms {
		rooms = append(rooms, rm)
	}
	s.mu.RUnlock()
	out := []engine.RoomInfo{}
	for _, rm := range rooms {
		out = append(out, rm.Info())
	}
	slices.SortFunc(out, func(a, b engine.RoomInfo) int { return b.Created.Compare(a.Created) })
	reply(w, 200, out)
}
func (s *Server) roomInfo(w http.ResponseWriter, r *http.Request) {
	rm := s.room(w, r)
	if rm != nil {
		reply(w, 200, rm.Info())
	}
}
func (s *Server) createRoom(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ScriptID string   `json:"script_id"`
		AISeats  []string `json:"ai_seats"`
		LLM      string   `json:"llm"`
	}
	if !decode(w, r, &req) {
		return
	}
	p := s.scripts[req.ScriptID]
	if p == nil {
		fail(w, 400, errors.New("unknown script_id"))
		return
	}
	if req.LLM == "" {
		req.LLM = s.DefaultLLM
	}
	if req.LLM != "mock" && req.LLM != "qoder" {
		fail(w, 400, errors.New("llm must be mock or qoder"))
		return
	}
	seen := map[string]bool{}
	for _, pid := range req.AISeats {
		if p.Character(pid) == nil || seen[pid] {
			fail(w, 400, errors.New("invalid or duplicate AI seat"))
			return
		}
		seen[pid] = true
	}
	if len(req.AISeats) > 0 && s.StartAI == nil {
		fail(w, 503, errors.New("AI provider unavailable"))
		return
	}
	rm, err := engine.NewRoom(engine.NewToken(), p, s.registry, s.store)
	if err != nil {
		fail(w, 500, err)
		return
	}
	for _, pid := range req.AISeats {
		_, err = rm.Join(pid, p.Character(pid).Name+"（AI）", "ai")
		if err != nil {
			rm.Close()
			fail(w, 500, err)
			return
		}
	}
	s.mu.Lock()
	s.rooms[rm.ID] = rm
	s.mu.Unlock()
	for _, pid := range req.AISeats {
		s.StartAI(s.ctx, rm, pid, req.LLM)
	}
	reply(w, 201, map[string]string{"room_id": rm.ID, "host_token": rm.HostToken})
}
func (s *Server) join(w http.ResponseWriter, r *http.Request) {
	rm := s.room(w, r)
	if rm == nil {
		return
	}
	var req struct {
		CharID string `json:"char_id"`
		Name   string `json:"name"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len([]rune(req.Name)) > 40 || strings.TrimSpace(req.Name) == "" {
		fail(w, 400, errors.New("name must be 1–40 characters"))
		return
	}
	tok, err := rm.Join(req.CharID, req.Name, "human")
	if err != nil {
		fail(w, 409, err)
		return
	}
	reply(w, 201, map[string]string{"room_id": rm.ID, "player_id": req.CharID, "token": tok})
}
func (s *Server) view(w http.ResponseWriter, r *http.Request) {
	rm := s.room(w, r)
	if rm == nil {
		return
	}
	pid, ok := s.auth(w, r, rm, false)
	if ok {
		reply(w, 200, rm.View(pid))
	}
}
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	rm := s.room(w, r)
	if rm == nil {
		return
	}
	pid, ok := s.auth(w, r, rm, false)
	if !ok {
		return
	}
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	secs, _ := strconv.Atoi(r.URL.Query().Get("wait"))
	if secs > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(min(secs, 25))*time.Second)
		defer cancel()
		rm.Wait(ctx, since)
	}
	ev := rm.Log(pid, since)
	if ev == nil {
		ev = []engine.Event{}
	}
	reply(w, 200, ev)
}
func (s *Server) action(w http.ResponseWriter, r *http.Request) {
	rm := s.room(w, r)
	if rm == nil {
		return
	}
	pid, ok := s.auth(w, r, rm, false)
	if !ok {
		return
	}
	var a engine.Action
	if !decode(w, r, &a) {
		return
	}
	a.Actor = pid
	if a.StateID <= 0 {
		fail(w, 400, errors.New("state_id required"))
		return
	}
	if err := rm.Submit(a); err != nil {
		fail(w, 409, err)
		return
	}
	reply(w, 200, rm.View(pid))
}
func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
	rm := s.room(w, r)
	if rm == nil {
		return
	}
	pid, ok := s.auth(w, r, rm, true)
	if !ok {
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(64 << 10)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	sub := rm.Subscribe(pid, 256)
	defer sub.Close()
	out := make(chan any, 256)
	go func() {
		defer cancel()
		for {
			_, b, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var msg struct {
				T      string        `json:"t"`
				ID     string        `json:"id"`
				Action engine.Action `json:"action"`
			}
			if err = json.Unmarshal(b, &msg); err != nil {
				continue
			}
			if msg.T != "action" {
				continue
			}
			msg.Action.Actor = pid
			if msg.Action.StateID <= 0 {
				err = errors.New("state_id required")
			} else {
				err = rm.Submit(msg.Action)
			}
			ack := map[string]any{"t": "ack", "id": msg.ID, "ok": err == nil}
			if err != nil {
				ack["error"] = err.Error()
			}
			select {
			case out <- ack:
			case <-ctx.Done():
				return
			}
		}
	}()
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	write := func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		c, stop := context.WithTimeout(ctx, 10*time.Second)
		defer stop()
		return conn.Write(c, websocket.MessageText, b)
	}
	for _, e := range rm.Log(pid, since) {
		if err = write(map[string]any{"t": "event", "event": e}); err != nil {
			return
		}
	}
	for {
		select {
		case m, ok := <-sub.C:
			if !ok {
				return
			}
			var obj any
			if m.View != nil {
				obj = map[string]any{"t": "view", "view": m.View}
			} else {
				obj = map[string]any{"t": "event", "event": m.Event}
			}
			if err = write(obj); err != nil {
				return
			}
		case m := <-out:
			if err = write(m); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}
func (s *Server) scriptAsset(w http.ResponseWriter, r *http.Request) {
	if s.assetDir == "" {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/script-assets/")
	if name == "" {
		http.NotFound(w, r)
		return
	}
	clean := filepath.Clean("/" + name)
	target := filepath.Join(s.assetDir, clean)
	rel, err := filepath.Rel(s.assetDir, target)
	if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
		http.NotFound(w, r)
		return
	}
	if fi, err := os.Stat(target); err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, target)
}

func (s *Server) assets(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ws/") {
		http.NotFound(w, r)
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		w.WriteHeader(405)
		return
	}
	if s.static == "" {
		reply(w, 200, map[string]string{"service": "jbs", "frontend": "run npm run dev in web/"})
		return
	}
	clean := filepath.Clean("/" + r.URL.Path)
	target := filepath.Join(s.static, clean)
	rel, err := filepath.Rel(s.static, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		http.NotFound(w, r)
		return
	}
	if fi, err := os.Stat(target); err == nil && !fi.IsDir() {
		http.ServeFile(w, r, target)
		return
	}
	if filepath.Ext(clean) != "" {
		http.NotFound(w, r)
		return
	}
	index := filepath.Join(s.static, "index.html")
	if _, err := os.Stat(index); err != nil {
		log.Print("frontend missing: ", err)
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, index)
}
