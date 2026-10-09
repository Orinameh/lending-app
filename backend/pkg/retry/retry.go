package retry

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"time"

	"github.com/lib/pq"
)

// Retriable reports whether err is a transient Postgres failure worth
// retrying: serialization failure (40001), deadlock (40P01), lock timeout
// (55P03), statement timeout (57014 when ours is bounded), connection
// failures (08000-08P01), or insufficient resources (53000/53100/53200).
func Retriable(err error) bool {
	if err == nil {
		return false
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		switch pqErr.Code {
		case "40001", "40P01", "55P03", "57014",
			"08000", "08003", "08006", "08001", "08004", "08P01",
			"53000", "53100", "53200", "53300":
			return true
		}
		return false
	}
	// database/sql wraps driver errors opaquely; match common substrings.
	msg := err.Error()
	for _, s := range []string{
		"connection refused", "connection reset", "broken pipe",
		"too many clients", "deadlock detected",
		"could not serialize access",
	} {
		if len(msg) >= len(s) && containsFold(msg, s) {
			return true
		}
	}
	return false
}

func containsFold(hay, needle string) bool {
	if len(needle) > len(hay) {
		return false
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			a, b := hay[i+j], needle[j]
			if 'A' <= a && a <= 'Z' {
				a += 'a' - 'A'
			}
			if 'A' <= b && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

type Options struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

func DefaultOptions() Options {
	return Options{MaxAttempts: 4, BaseDelay: 50 * time.Millisecond, MaxDelay: 2 * time.Second}
}

func backoff(attempt int, base, max time.Duration) time.Duration {
	d := base << attempt
	if d > max || d <= 0 {
		d = max
	}
	// Full jitter.
	n, err := rand.Int(rand.Reader, big.NewInt(int64(d)+1))
	if err != nil {
		return d / 2
	}
	return time.Duration(n.Int64())
}

// Do runs fn until success, a non-retriable error, context cancellation, or
// MaxAttempts exhausted. The last error is returned wrapped.
func Do(ctx context.Context, opt Options, fn func(ctx context.Context) error) error {
	if opt.MaxAttempts < 1 {
		opt.MaxAttempts = 1
	}
	var err error
	for attempt := 0; attempt < opt.MaxAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err = fn(ctx); err == nil {
			return nil
		}
		if !Retriable(err) {
			return err
		}
		if attempt == opt.MaxAttempts-1 {
			break
		}
		delay := backoff(attempt, opt.BaseDelay, opt.MaxDelay)
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	return err
}
