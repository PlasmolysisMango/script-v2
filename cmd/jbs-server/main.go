package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"jbs/internal/ai"
	"jbs/internal/ai/llm"
	"jbs/internal/engine"
	"jbs/internal/plugins"
	"jbs/internal/server"
	"jbs/internal/store"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	scripts := flag.String("scripts", "scripts", "script package directory")
	scriptAssets := flag.String("script-assets", "", "static script asset directory (default <scripts>/imported/assets)")
	strict := flag.Bool("strict-scripts", false, "fail startup when any script fails to load")
	static := flag.String("static", "web/dist", "built React directory")
	data := flag.String("data", "data", "private room log directory")
	backend := flag.String("llm", "qoder", "default AI provider: mock or qoder")
	executable := flag.String("qodercli", "qodercli", "qodercli executable")
	model := flag.String("model", "", "optional qodercli model")
	flag.Parse()
	if *backend != "mock" && *backend != "qoder" {
		return errors.New("llm must be mock or qoder")
	}
	reg, err := plugins.NewRegistry()
	if err != nil {
		return err
	}
	persistence, err := store.New(filepath.Join(*data, "rooms"))
	if err != nil {
		return err
	}
	defer persistence.Close()
	app, err := server.New(reg, persistence, *scripts, *static, server.Options{AssetsDir: *scriptAssets, Strict: *strict})
	if err != nil {
		return err
	}
	defer app.Close()
	app.DefaultLLM = *backend
	qoder := &llm.Qoder{Executable: *executable, Model: *model, Semaphore: llm.NewSemaphore(2)}
	app.StartAI = func(ctx context.Context, room *engine.Room, pid, provider string) {
		var p llm.Provider = qoder
		if provider == "mock" {
			p = llm.Mock{}
		}
		ai.Start(ctx, room, pid, p, ai.Options{OnResult: func(result ai.Result) {
			b, _ := json.Marshal(result)
			var payload map[string]any
			_ = json.Unmarshal(b, &payload)
			room.Record(pid, "ai.result", payload)
		}})
	}
	srv := &http.Server{Addr: *addr, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("jbs listening on %s; scripts=%s; default AI=%s", *addr, *scripts, *backend)
	if err = srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	if err = persistence.Err(); err != nil {
		return err
	}
	return nil
}
