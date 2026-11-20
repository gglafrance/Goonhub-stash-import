// Package httpx holds the retry policy shared by the Stash and GoonHub clients.
package httpx

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"time"
)

type Policy struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// StatusError is returned for any non-2xx response.
type StatusError struct {
	Method string
	URL    string
	Status int
	Body   string
}

func (e *StatusError) Error() string {
	body := e.Body
	if len(body) > 500 {
		body = body[:500] + "..."
	}
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.URL, e.Status, body)
}

func (e *StatusError) HTTPStatus() int { return e.Status }

// HTTPStatusError is implemented by every error carrying an HTTP response status.
type HTTPStatusError interface {
	error
	HTTPStatus() int
}

// StatusOf returns the HTTP status of err, or 0.
func StatusOf(err error) int {
	var se HTTPStatusError
	if errors.As(err, &se) {
		return se.HTTPStatus()
	}
	return 0
}

// Retryable reports whether err is worth retrying. When idempotent is false only
// failures where the server most likely did not process the request are retried.
func Retryable(err error, idempotent bool) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var se HTTPStatusError
	if errors.As(err, &se) {
		switch se.HTTPStatus() {
		case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable:
			return true
		case http.StatusInternalServerError, http.StatusGatewayTimeout:
			return idempotent
		}
		return false
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return true // connection never established
	}
	// Timeouts, resets, EOF: the request may have been processed.
	return idempotent
}

// Do runs fn until it succeeds, fails with a non-retryable error, or attempts run out.
func (p Policy) Do(ctx context.Context, idempotent bool, fn func() error) error {
	attempts := max(p.MaxAttempts, 1)
	backoff := p.InitialBackoff
	var err error
	for i := 0; i < attempts; i++ {
		if err = fn(); err == nil || !Retryable(err, idempotent) || i == attempts-1 {
			return err
		}
		jitter := time.Duration(rand.Int64N(int64(backoff/2) + 1))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff + jitter):
		}
		backoff = min(backoff*2, p.MaxBackoff)
	}
	return err
}
