// Package retry provides utilities for retrying operations with exponential backoff
package retry

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"time"
)

// Config represents retry configuration
type Config struct {
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	Multiplier     float64
	Jitter         float64 // 0.0-1.0, fraction of backoff to randomize (e.g. 0.25 = +/-25%)
}

// DefaultConfig returns a default retry configuration
func DefaultConfig() *Config {
	return &Config{
		MaxRetries:     3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		Multiplier:     2.0,
		Jitter:         0.25,
	}
}

// computeBackoff calculates backoff with optional jitter
func computeBackoff(cfg *Config, attempt int) time.Duration {
	backoff := time.Duration(float64(cfg.InitialBackoff) * math.Pow(cfg.Multiplier, float64(attempt)))
	if backoff > cfg.MaxBackoff {
		backoff = cfg.MaxBackoff
	}
	if cfg.Jitter > 0 {
		jitterRange := float64(backoff) * cfg.Jitter
		backoff = time.Duration(float64(backoff) + jitterRange*(2*rand.Float64()-1))
	}
	if backoff < time.Millisecond {
		backoff = time.Millisecond
	}
	return backoff
}

// RetryableFunc is a function that can be retried
type RetryableFunc func() error

// RetryableError indicates whether an error is retryable
type RetryableError interface {
	error
	IsRetryable() bool
}

type retryableError struct {
	err       error
	retryable bool
}

func (e *retryableError) Error() string {
	return e.err.Error()
}

func (e *retryableError) IsRetryable() bool {
	return e.retryable
}

func (e *retryableError) Unwrap() error {
	return e.err
}

// Retryable wraps an error to make it retryable
func Retryable(err error) error {
	return &retryableError{err: err, retryable: true}
}

// NonRetryable wraps an error to make it non-retryable
func NonRetryable(err error) error {
	return &retryableError{err: err, retryable: false}
}

// Do executes the function with retries according to the config.
func Do(ctx context.Context, cfg *Config, fn RetryableFunc) error {
	return DoWithNotify(ctx, cfg, fn, nil)
}

// DoWithNotify executes the function with retries and calls notify on each retry.
func DoWithNotify(ctx context.Context, cfg *Config, fn RetryableFunc, notify func(error, time.Duration)) error {
	var lastErr error

	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		err := fn()

		if err == nil {
			return nil
		}

		lastErr = err

		if retryErr, ok := errors.AsType[RetryableError](err); ok && !retryErr.IsRetryable() {
			return err
		}

		if attempt == cfg.MaxRetries {
			break
		}

		backoff := computeBackoff(cfg, attempt)

		if notify != nil {
			notify(err, backoff)
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("retry cancelled: %w", ctx.Err())
		case <-timer.C:
		}
	}

	return fmt.Errorf("max retries exceeded: %w", lastErr)
}
