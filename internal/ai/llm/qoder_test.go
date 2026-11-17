package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// Tests use this test binary as a fake executable, never a real qodercli.
func fakeExecutable(t *testing.T, mode string) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("JBS_QODER_HELPER", mode)
	t.Setenv("JBS_QODER_TEST_BINARY", executable)
	path := filepath.Join(t.TempDir(), "fake-qodercli")
	body := "#!/bin/sh\nexec \"$JBS_QODER_TEST_BINARY\" -test.run=TestQoderHelperProcess -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestQoderHelperProcess(t *testing.T) {
	mode := os.Getenv("JBS_QODER_HELPER")
	if mode == "" {
		return
	}
	args := os.Args
	idx := slicesIndex(args, "--")
	args = args[idx+1:]
	want := []string{"-p", "--no-session-persistence", "--tools", "", "-o", "json", "--system-prompt", "系统", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--model", "test-model"}
	if !reflect.DeepEqual(args, want) {
		fmt.Fprintln(os.Stderr, "secret-token: bad arguments")
		os.Exit(2)
	}
	b, _ := io.ReadAll(os.Stdin)
	if string(b) != "输入提示" {
		os.Exit(3)
	}
	entries, err := os.ReadDir(".")
	if err != nil || len(entries) != 0 {
		os.Exit(4)
	}
	switch mode {
	case "ok":
		fmt.Fprintln(os.Stdout, `{"type":"result","result":"完成","is_error":false,"ignored":"secret-token"}`)
	case "provider-error":
		fmt.Fprintln(os.Stdout, `{"result":"secret-token","is_error":true}`)
	case "exit":
		fmt.Fprintln(os.Stdout, "secret-token")
		fmt.Fprintln(os.Stderr, "secret-token")
		os.Exit(7)
	case "invalid":
		fmt.Fprintln(os.Stdout, "secret-token not JSON")
	case "limit":
		_, _ = io.WriteString(os.Stdout, strings.Repeat("x", MaxOutputBytes+1))
	case "timeout":
		time.Sleep(10 * time.Second)
	}
	os.Exit(0)
}

func slicesIndex(ss []string, target string) int {
	for i, s := range ss {
		if s == target {
			return i
		}
	}
	return -1
}

func TestQoderComplete(t *testing.T) {
	for _, tc := range []struct {
		mode    string
		wantErr error
	}{
		{"ok", nil}, {"provider-error", ErrProvider}, {"exit", ErrExecution},
		{"invalid", ErrResponse}, {"limit", ErrOutputLimit}, {"timeout", context.DeadlineExceeded},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			q := &Qoder{Executable: fakeExecutable(t, tc.mode), Model: "test-model"}
			if tc.mode == "timeout" {
				q.Timeout = 100 * time.Millisecond
			}
			text, err := q.Complete(context.Background(), "系统", "输入提示")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && text != "完成" {
				t.Fatalf("bad result %q", text)
			}
			if err != nil && (strings.Contains(err.Error(), "secret-token") || text != "") {
				t.Fatal("provider error leaked raw output")
			}
		})
	}
}

func TestQoderMissingExecutableAndCancellation(t *testing.T) {
	q := &Qoder{Executable: filepath.Join(t.TempDir(), "absent")}
	if _, err := q.Complete(context.Background(), "", ""); !errors.Is(err, ErrExecution) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := q.Complete(ctx, "", ""); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestParseResponse(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{}`, `{"result":12}`, `{"result":""}`, `{"result":"x"} trailing`} {
		if _, err := parseResponse([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestSemaphoreLimitsAndCancellation(t *testing.T) {
	s := NewSemaphore(99)
	ctx := context.Background()
	if err := s.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	blocked, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := s.acquire(blocked); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("third slot acquired: %v", err)
	}
	s.release()
	s.release()
	var mu sync.Mutex
	active, peak := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.acquire(ctx); err != nil {
				return
			}
			mu.Lock()
			active++
			peak = max(peak, active)
			mu.Unlock()
			time.Sleep(time.Millisecond)
			mu.Lock()
			active--
			mu.Unlock()
			s.release()
		}()
	}
	wg.Wait()
	if peak > 2 || peak == 0 {
		t.Fatalf("invalid peak %d", peak)
	}
}

func TestAllQoderInstancesShareProcessCeiling(t *testing.T) {
	ctx := context.Background()
	if err := processSemaphore.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	defer processSemaphore.release()
	if err := processSemaphore.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	defer processSemaphore.release()
	for _, pool := range []*Semaphore{nil, NewSemaphore(2), new(Semaphore)} {
		q := &Qoder{Executable: "not-used-while-pool-full", Semaphore: pool, Timeout: 20 * time.Millisecond}
		if _, err := q.Complete(ctx, "", ""); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("separate instance bypassed shared ceiling: %v", err)
		}
	}
}

func TestMockFirstAvailableVote(t *testing.T) {
	prompt := `{"mode":"turn","action_options":[{"type":"vote.cast","params":{"question":"id","option":"value"},"options":[{"value":"q"}]}],"data":{"votes":{"questions":[{"id":"q","options":[{"value":"first"},{"value":"correct-but-not-first"}]}],"cast":{}}}}`
	text, err := (Mock{}).Complete(context.Background(), "", prompt)
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Action  string
		Payload map[string]string
	}
	if json.Unmarshal([]byte(text), &d) != nil || d.Action != "vote.cast" || d.Payload["question"] != "q" || d.Payload["option"] != "first" {
		t.Fatalf("bad deterministic vote: %s", text)
	}
}
