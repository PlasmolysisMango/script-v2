package ai

import "sync"

// Result is a private operational diagnostic. It excludes prompts, payloads,
// thoughts, raw provider errors and credentials. Do not broadcast OnResult.
type Result struct {
	RoomID     string `json:"room_id"`
	PID        string `json:"pid"`
	Fallback   bool   `json:"fallback"`
	Error      string `json:"error,omitempty"`
	Provider   string `json:"provider"`
	Action     string `json:"action"`
	DurationMS int64  `json:"duration_ms"`
	Submitted  bool   `json:"submitted"`
}

type StatsSnapshot struct {
	Results    uint64 `json:"results"`
	Submitted  uint64 `json:"submitted"`
	Fallbacks  uint64 `json:"fallbacks"`
	Errors     uint64 `json:"errors"`
	DurationMS int64  `json:"duration_ms"`
	Last       Result `json:"last"`
}

// Stats is concurrency safe; its zero value is ready to use.
type Stats struct {
	mu sync.Mutex
	v  StatsSnapshot
}

var DefaultStats = &Stats{}

func (s *Stats) Snapshot() StatsSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.v
}

func (s *Stats) record(r Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.v.Results++
	if r.Submitted {
		s.v.Submitted++
	}
	if r.Fallback {
		s.v.Fallbacks++
	}
	if r.Error != "" {
		s.v.Errors++
	}
	s.v.DurationMS += r.DurationMS
	s.v.Last = r
}
