package enrichment

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type RateLimitError struct{ RetryAfter time.Duration }

func (RateLimitError) Error() string { return "OpenAI usage or rate limit reached" }

type responseError struct {
	Code string `json:"code"`
	Type string `json:"type"`
}

func (e responseError) rateLimited() bool { return rateLimitCode(e.Code) || rateLimitCode(e.Type) }
func rateLimitCode(code string) bool {
	switch code {
	case "rate_limit_exceeded", "usage_limit_exceeded", "insufficient_quota", "subscription_sharing_usage_limit_exceeded":
		return true
	}
	return false
}

func retryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 {
		// Saturate rather than overflow on an untrusted response header.
		const maxSeconds = int64((1<<63 - 1) / time.Second)
		if seconds > maxSeconds {
			seconds = maxSeconds
		}
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return 0
}

// One gate per run prevents all sixteen workers retrying together. Once a
// cooldown expires, only one request probes availability; success releases the
// others. Already-running requests can finish and their results can be saved.
type retryGate struct {
	mu                   sync.Mutex
	base, maximum, delay time.Duration
	until                time.Time
	generation           uint64
	cooling, probing     bool
	changed              chan struct{}
}

type retryTicket struct {
	generation uint64
	probe      bool
}

func newRetryGate() *retryGate {
	return &retryGate{base: 2 * time.Second, maximum: 5 * time.Minute, changed: make(chan struct{})}
}

func (g *retryGate) signal() { close(g.changed); g.changed = make(chan struct{}) }

// waiting receives a zero time while another request is probing availability.
func (g *retryGate) acquire(ctx context.Context, waiting func(time.Time)) (retryTicket, error) {
	for {
		if err := ctx.Err(); err != nil {
			return retryTicket{}, err
		}
		g.mu.Lock()
		if !g.cooling {
			ticket := retryTicket{generation: g.generation}
			g.mu.Unlock()
			return ticket, nil
		}
		remaining := time.Until(g.until)
		if remaining <= 0 && !g.probing {
			g.probing = true
			ticket := retryTicket{generation: g.generation, probe: true}
			g.mu.Unlock()
			return ticket, nil
		}
		changed, until := g.changed, g.until
		g.mu.Unlock()
		var timer *time.Timer
		var elapsed <-chan time.Time
		if remaining > 0 {
			timer = time.NewTimer(remaining)
			elapsed = timer.C
		} else {
			until = time.Time{}
		}
		if waiting != nil {
			waiting(until)
		}
		select {
		case <-ctx.Done():
		case <-changed:
		case <-elapsed:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

func (g *retryGate) finish(ticket retryTicket, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var limited RateLimitError
	if errors.As(err, &limited) {
		if ticket.generation == g.generation {
			// Count one rejection per cooldown, not once per in-flight worker.
			g.delay = min(g.maximum, max(g.base, g.delay*2))
			jitter := time.Duration(rand.Int64N(int64(g.delay/4) + 1))
			g.until = time.Now().Add(min(g.maximum, g.delay+jitter))
			g.generation++
			g.cooling, g.probing = true, false
		}
		// A later server deadline still applies to requests already in flight.
		if until := time.Now().Add(limited.RetryAfter); limited.RetryAfter > 0 && until.After(g.until) {
			g.until = until
			// A probe already in flight must not erase this newer deadline.
			g.generation++
			g.probing = false
			g.cooling = true
		}
		g.signal()
	} else if ticket.probe && ticket.generation == g.generation {
		// Any non-rate-limit response ends the cooldown. Its normal handling
		// still applies (for example, authentication errors stop the run).
		g.cooling, g.probing, g.delay = false, false, 0
		g.generation++
		g.signal()
	}
}

// Only explicit rate/usage rejections are retried. Waiting is outside each
// request's timeout, remains cancellable, and continues until recovery or Stop.
func (g *retryGate) call(ctx context.Context, waiting func(time.Time), attempt func(context.Context) error) error {
	for {
		ticket, err := g.acquire(ctx, waiting)
		if err != nil {
			return err
		}
		callCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		err = attempt(callCtx)
		cancel()
		g.finish(ticket, err)
		var limited RateLimitError
		if !errors.As(err, &limited) {
			return err
		}
	}
}

func retryMessage(until time.Time) string {
	if until.IsZero() {
		return "Waiting for the OpenAI retry to finish…"
	}
	return "OpenAI usage or rate limit reached. Retrying automatically after " + until.Format("Jan 2, 15:04:05") + "."
}
