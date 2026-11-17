// Package llm supplies text completion providers without access to game scripts.
package llm

import (
	"context"
	"errors"
)

type Provider interface {
	Complete(ctx context.Context, system, prompt string) (string, error)
}

var (
	ErrExecution   = errors.New("provider execution failed")
	ErrOutputLimit = errors.New("provider output exceeds limit")
	ErrResponse    = errors.New("invalid provider response")
	ErrProvider    = errors.New("provider reported an error")
)

// Name is safe to expose in diagnostics; it never includes configuration.
func Name(p Provider) string {
	switch p.(type) {
	case *Qoder:
		return "qodercli"
	case Mock, *Mock:
		return "mock"
	default:
		return "custom"
	}
}

// ErrorCode deliberately does not expose provider messages, stderr, or tokens.
func ErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, ErrOutputLimit):
		return "output_limit"
	case errors.Is(err, ErrResponse):
		return "invalid_response"
	case errors.Is(err, ErrExecution):
		return "execution_failed"
	default:
		return "provider_error"
	}
}
