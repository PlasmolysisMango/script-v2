package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const MaxOutputBytes = 1 << 20
const DefaultTimeout = 90 * time.Second

// Semaphore is a cancellation-aware process pool. Its zero value permits two
// processes. All Qoder instances additionally share the process-wide ceiling.
type Semaphore struct {
	once sync.Once
	size int
	ch   chan struct{}
}

func NewSemaphore(size int) *Semaphore {
	return &Semaphore{size: max(1, min(size, 2))}
}

func (s *Semaphore) acquire(ctx context.Context) error {
	s.once.Do(func() {
		n := s.size
		if n == 0 {
			n = 2
		}
		s.ch = make(chan struct{}, n)
	})
	select {
	case s.ch <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-s.ch
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Semaphore) release() { <-s.ch }

var processSemaphore = NewSemaphore(2)

// Qoder runs a fresh, tool-free qodercli completion in an empty temporary cwd.
// Configuration must not be modified while Complete is running. A nil Semaphore
// shares the default pool. Timeout may shorten, but not exceed, ninety seconds.
type Qoder struct {
	Executable string
	Model      string
	Semaphore  *Semaphore
	Timeout    time.Duration
}

func (q *Qoder) Complete(ctx context.Context, system, prompt string) (string, error) {
	timeout := q.Timeout
	if timeout <= 0 || timeout > DefaultTimeout {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if q.Semaphore != nil && q.Semaphore != processSemaphore {
		if err := q.Semaphore.acquire(ctx); err != nil {
			return "", err
		}
		defer q.Semaphore.release()
	}
	if err := processSemaphore.acquire(ctx); err != nil {
		return "", err
	}
	defer processSemaphore.release()

	executable := q.Executable
	if executable == "" {
		executable = "qodercli"
	}
	// Resolve before changing cwd, including caller-supplied relative paths.
	var err error
	if strings.ContainsRune(executable, filepath.Separator) {
		executable, err = filepath.Abs(executable)
	} else {
		executable, err = exec.LookPath(executable)
	}
	if err != nil {
		return "", ErrExecution
	}
	cwd, err := os.MkdirTemp("", "jbs-ai-")
	if err != nil {
		return "", ErrExecution
	}
	defer os.RemoveAll(cwd)
	args := []string{"-p", "--no-session-persistence", "--tools", "", "-o", "json",
		"--system-prompt", system, "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`}
	if q.Model != "" {
		args = append(args, "--model", q.Model)
	}
	// Do not add --setting-sources: it is not supported by every qodercli build.
	// No permission-bypass flags are used. The empty cwd excludes project config.
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = cwd
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	out := &limitedOutput{cancel: cancel}
	cmd.Stdout = out
	err = cmd.Run()
	if out.exceeded {
		return "", ErrOutputLimit
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", ErrExecution
	}
	return parseResponse(out.buf.Bytes())
}

type limitedOutput struct {
	buf      bytes.Buffer
	exceeded bool
	cancel   context.CancelFunc
}

func (w *limitedOutput) Write(p []byte) (int, error) {
	n := min(len(p), MaxOutputBytes-w.buf.Len())
	_, _ = w.buf.Write(p[:n])
	if n != len(p) {
		w.exceeded = true
		w.cancel()
		return n, ErrOutputLimit
	}
	return n, nil
}

func parseResponse(b []byte) (string, error) {
	var wrapper struct {
		Result  *string `json:"result"`
		IsError bool    `json:"is_error"`
	}
	if len(b) > MaxOutputBytes {
		return "", ErrOutputLimit
	}
	if json.Unmarshal(b, &wrapper) != nil {
		return "", ErrResponse
	}
	if wrapper.IsError {
		return "", ErrProvider
	}
	if wrapper.Result == nil || strings.TrimSpace(*wrapper.Result) == "" {
		return "", ErrResponse
	}
	return *wrapper.Result, nil
}
