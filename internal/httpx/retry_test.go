package httpx

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

type conflict struct{}

func (conflict) Error() string   { return "conflict" }
func (conflict) HTTPStatus() int { return 409 }

func TestRetryable(t *testing.T) {
	dial := &net.OpError{Op: "dial", Err: errors.New("refused")}
	tests := []struct {
		name       string
		err        error
		idempotent bool
		want       bool
	}{
		{"409 never", conflict{}, true, false},
		{"400 never", &StatusError{Status: 400}, true, false},
		{"401 never", &StatusError{Status: 401}, true, false},
		{"503 always", &StatusError{Status: 503}, false, true},
		{"500 idempotent", &StatusError{Status: 500}, true, true},
		{"500 non-idempotent", &StatusError{Status: 500}, false, false},
		{"dial always", dial, false, true},
		{"timeout idempotent", errors.New("i/o timeout"), true, true},
		{"timeout non-idempotent", errors.New("i/o timeout"), false, false},
		{"canceled", context.Canceled, true, false},
	}
	for _, tt := range tests {
		if got := Retryable(tt.err, tt.idempotent); got != tt.want {
			t.Fatalf("%s: Retryable = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestDoStopsOnNonRetryable(t *testing.T) {
	calls := 0
	p := Policy{MaxAttempts: 5, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond}
	_ = p.Do(context.Background(), true, func() error { calls++; return conflict{} })
	if calls != 1 {
		t.Fatalf("a 409 must not be retried, got %d calls", calls)
	}
	calls = 0
	_ = p.Do(context.Background(), true, func() error { calls++; return &StatusError{Status: 503} })
	if calls != 5 {
		t.Fatalf("a 503 must be retried up to MaxAttempts, got %d calls", calls)
	}
}
