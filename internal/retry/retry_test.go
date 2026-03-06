package retry

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDo_Success(t *testing.T) {
	ctx := t.Context()
	cfg := &Config{
		MaxRetries:     3,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		Multiplier:     2.0,
	}

	attempts := 0
	err := Do(ctx, cfg, func() error {
		attempts++
		return nil
	})

	if err != nil {
		t.Errorf("Do() unexpected error = %v", err)
	}
	if attempts != 1 {
		t.Errorf("Do() attempts = %d, want 1", attempts)
	}
}

func TestDo_RetryableError(t *testing.T) {
	ctx := t.Context()
	cfg := &Config{
		MaxRetries:     3,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		Multiplier:     2.0,
	}

	attempts := 0
	err := Do(ctx, cfg, func() error {
		attempts++
		if attempts < 3 {
			return Retryable(errors.New("temporary error"))
		}
		return nil
	})

	if err != nil {
		t.Errorf("Do() unexpected error = %v", err)
	}
	if attempts != 3 {
		t.Errorf("Do() attempts = %d, want 3", attempts)
	}
}

func TestDo_NonRetryableError(t *testing.T) {
	ctx := t.Context()
	cfg := &Config{
		MaxRetries:     3,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		Multiplier:     2.0,
	}

	attempts := 0
	expectedErr := errors.New("permanent error")
	err := Do(ctx, cfg, func() error {
		attempts++
		return NonRetryable(expectedErr)
	})

	if err == nil {
		t.Error("Do() expected error but got none")
	}
	if attempts != 1 {
		t.Errorf("Do() attempts = %d, want 1 (should not retry non-retryable errors)", attempts)
	}
}

func TestDo_MaxRetriesExceeded(t *testing.T) {
	ctx := t.Context()
	cfg := &Config{
		MaxRetries:     2,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		Multiplier:     2.0,
	}

	attempts := 0
	err := Do(ctx, cfg, func() error {
		attempts++
		return errors.New("always fails")
	})

	if err == nil {
		t.Error("Do() expected error but got none")
	}
	if attempts != 3 { // Initial attempt + 2 retries
		t.Errorf("Do() attempts = %d, want 3", attempts)
	}
	if !strings.Contains(err.Error(), "max retries exceeded") {
		t.Errorf("Do() error should mention max retries exceeded")
	}
}

func TestDo_ContextCancellation(t *testing.T) {
	cfg := &Config{
		MaxRetries:     5,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     1 * time.Second,
		Multiplier:     2.0,
	}

	ctx, cancel := context.WithCancel(t.Context())

	attempts := 0
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := Do(ctx, cfg, func() error {
		attempts++
		return errors.New("temporary error")
	})

	if err == nil {
		t.Error("Do() expected error but got none")
	}
	if !strings.Contains(err.Error(), "retry cancelled") {
		t.Errorf("Do() error should mention cancellation, got: %v", err)
	}
	// Should have attempted at least once
	if attempts < 1 {
		t.Errorf("Do() attempts = %d, want at least 1", attempts)
	}
}

func TestDoWithNotify(t *testing.T) {
	ctx := t.Context()
	cfg := &Config{
		MaxRetries:     2,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		Multiplier:     2.0,
	}

	attempts := 0
	notifications := 0
	var lastBackoff time.Duration

	err := DoWithNotify(ctx, cfg, func() error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary error")
		}
		return nil
	}, func(err error, backoff time.Duration) {
		notifications++
		lastBackoff = backoff
	})

	if err != nil {
		t.Errorf("DoWithNotify() unexpected error = %v", err)
	}
	if attempts != 3 {
		t.Errorf("DoWithNotify() attempts = %d, want 3", attempts)
	}
	if notifications != 2 {
		t.Errorf("DoWithNotify() notifications = %d, want 2", notifications)
	}
	if lastBackoff == 0 {
		t.Error("DoWithNotify() notify function should receive non-zero backoff")
	}
}

func TestBackoffCalculation(t *testing.T) {
	ctx := t.Context()
	cfg := &Config{
		MaxRetries:     3,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     1 * time.Second,
		Multiplier:     2.0,
	}

	var backoffs []time.Duration
	err := DoWithNotify(ctx, cfg, func() error {
		return errors.New("always fail")
	}, func(err error, backoff time.Duration) {
		backoffs = append(backoffs, backoff)
	})

	if err == nil {
		t.Error("Expected error but got none")
	}

	// Check backoff progression
	expectedBackoffs := []time.Duration{
		100 * time.Millisecond, // Initial
		200 * time.Millisecond, // Initial * 2
		400 * time.Millisecond, // Initial * 4
	}

	if len(backoffs) != len(expectedBackoffs) {
		t.Fatalf("Got %d backoffs, want %d", len(backoffs), len(expectedBackoffs))
	}

	for i, backoff := range backoffs {
		if backoff != expectedBackoffs[i] {
			t.Errorf("Backoff[%d] = %v, want %v", i, backoff, expectedBackoffs[i])
		}
	}
}

func TestMaxBackoffLimit(t *testing.T) {
	ctx := t.Context()
	cfg := &Config{
		MaxRetries:     5,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     300 * time.Millisecond,
		Multiplier:     10.0, // Large multiplier to test max backoff
	}

	var backoffs []time.Duration
	_ = DoWithNotify(ctx, cfg, func() error {
		return errors.New("always fail")
	}, func(err error, backoff time.Duration) {
		backoffs = append(backoffs, backoff)
	})

	// All backoffs after hitting max should be capped
	for i, backoff := range backoffs {
		if backoff > cfg.MaxBackoff {
			t.Errorf("Backoff[%d] = %v exceeds MaxBackoff = %v", i, backoff, cfg.MaxBackoff)
		}
	}
}

func TestRetryableError_ErrorAndUnwrap(t *testing.T) {
	inner := errors.New("inner err")
	re := Retryable(inner)
	if re.Error() != "inner err" {
		t.Errorf("Error() = %q, want %q", re.Error(), "inner err")
	}
	retryErr, ok := errors.AsType[RetryableError](re)
	if !ok {
		t.Fatal("expected RetryableError interface")
	}
	if !retryErr.IsRetryable() {
		t.Error("expected retryable = true")
	}
	if !errors.Is(re, inner) {
		t.Error("Unwrap() should return inner error")
	}
}

func TestNonRetryableError_ErrorAndUnwrap(t *testing.T) {
	inner := errors.New("perm")
	nre := NonRetryable(inner)
	if nre.Error() != "perm" {
		t.Errorf("Error() = %q, want %q", nre.Error(), "perm")
	}
	retryErr, ok := errors.AsType[RetryableError](nre)
	if !ok {
		t.Fatal("expected RetryableError interface")
	}
	if retryErr.IsRetryable() {
		t.Error("expected retryable = false")
	}
	if !errors.Is(nre, inner) {
		t.Error("Unwrap() should return inner error")
	}
}

func TestComputeBackoff_WithJitter(t *testing.T) {
	cfg := &Config{
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     10 * time.Second,
		Multiplier:     2.0,
		Jitter:         0.5,
	}
	b := computeBackoff(cfg, 0)
	low := time.Duration(float64(100*time.Millisecond) * 0.5)
	high := time.Duration(float64(100*time.Millisecond) * 1.5)
	if b < low || b > high {
		t.Errorf("computeBackoff() = %v, want between %v and %v", b, low, high)
	}
}

func TestComputeBackoff_MinimumFloor(t *testing.T) {
	cfg := &Config{
		InitialBackoff: 1 * time.Nanosecond,
		MaxBackoff:     10 * time.Second,
		Multiplier:     1.0,
	}
	b := computeBackoff(cfg, 0)
	if b < time.Millisecond {
		t.Errorf("computeBackoff() = %v, want >= 1ms", b)
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.MaxRetries != 3 {
		t.Errorf("DefaultConfig().MaxRetries = %d, want 3", cfg.MaxRetries)
	}
	if cfg.InitialBackoff != 1*time.Second {
		t.Errorf("DefaultConfig().InitialBackoff = %v, want 1s", cfg.InitialBackoff)
	}
	if cfg.MaxBackoff != 30*time.Second {
		t.Errorf("DefaultConfig().MaxBackoff = %v, want 30s", cfg.MaxBackoff)
	}
	if cfg.Multiplier != 2.0 {
		t.Errorf("DefaultConfig().Multiplier = %f, want 2.0", cfg.Multiplier)
	}
}
