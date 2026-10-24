package httpclient

import (
	"context"
	"sync"
	"time"
)

// limiter serializes requests per indexer and enforces a minimum interval
// between them (SPEC-04 §3.2: 突发上限 1, 同索引器请求串行).
type limiter struct {
	mu          sync.Mutex
	last        time.Time // when the previous request was released
	delayFn     func() time.Duration
	testDelayFn func() time.Duration
}

func newLimiter(testDelayFn func() time.Duration, delayFn func() time.Duration) *limiter {
	return &limiter{delayFn: delayFn, testDelayFn: testDelayFn}
}

// setDelay swaps the delay provider (per-indexer requestDelay override).
func (l *limiter) setDelay(fn func() time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.delayFn = fn
}

// wait blocks until the request may be sent: it holds the mutex for the
// whole call (serializing same-indexer requests) and sleeps the remainder
// of the minimum interval since the previous release.
func (l *limiter) wait(ctx context.Context, isTest bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	delay := l.delayFn()
	if isTest && l.testDelayFn != nil {
		delay = l.testDelayFn()
	}
	if delay > 0 && !l.last.IsZero() {
		elapsed := time.Since(l.last)
		if remaining := delay - elapsed; remaining > 0 {
			timer := time.NewTimer(remaining)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}
	l.last = time.Now()
}
