package enrichment

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Rainbows4Blood/Nekonomicon/internal/enrichdata"
	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

func quickGate() *retryGate {
	g := newRetryGate()
	g.base, g.maximum = time.Millisecond, 10*time.Millisecond
	return g
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		header string
		want   time.Duration
	}{
		{" 120 ", 2 * time.Minute},
		{now.Add(time.Hour).Format(http.TimeFormat), time.Hour},
		{now.Add(-time.Hour).Format(http.TimeFormat), 0},
		{"-1", 0}, {"0", 0}, {"invalid", 0}, {"", 0},
		{"9223372036854775807", time.Duration((1<<63-1)/int64(time.Second)) * time.Second},
	} {
		if got := retryAfter(tc.header, now); got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.header, got, tc.want)
		}
	}
}

func TestRateLimitResponsesAreRetryableButAuthAndIncompleteAreNot(t *testing.T) {
	for _, status := range []int{429, 401, 403, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "600")
				w.WriteHeader(status)
				fmt.Fprint(w, "untrusted private error details")
			}))
			defer srv.Close()
			c := NewClient(func(context.Context) (string, error) { return "synthetic", nil })
			c.Base = srv.URL
			err := c.Check(context.Background())
			var limited RateLimitError
			if errors.As(err, &limited) != (status == 429) {
				t.Fatal(err)
			}
			if status == 429 && limited.RetryAfter != 10*time.Minute {
				t.Fatal(limited)
			}
			if err == nil || strings.Contains(err.Error(), "private") {
				t.Fatal("missing or unsanitized error", err)
			}
		})
	}
	for _, raw := range []string{
		`{"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded"}}}`,
		`{"type":"response.failed","response":{"error":{"code":"subscription_sharing_usage_limit_exceeded"}}}`,
		`{"type":"error","error":{"code":"insufficient_quota"}}`,
		`{"type":"error","error":{"type":"usage_limit_exceeded"}}`,
		`{"type":"error","code":"rate_limit_exceeded"}`,
	} {
		_, err := readStream(strings.NewReader("data: "+raw+"\n\n"), nil)
		var limited RateLimitError
		if !errors.As(err, &limited) {
			t.Fatal(raw, err)
		}
	}
	for _, kind := range []string{"response.incomplete", "response.completed"} {
		_, err := readStream(strings.NewReader(fmt.Sprintf("data: {\"type\":%q,\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"rate_limit_exceeded\"}}}\n\n", kind)), nil)
		var limited RateLimitError
		if err == nil || errors.As(err, &limited) {
			t.Fatal("invalid completion treated as retryable rejection", kind, err)
		}
	}
}

func TestSharedBackoffGrowsCapsAndHonorsNewerServerDeadline(t *testing.T) {
	g := newRetryGate()
	g.maximum = 8 * time.Second
	ticket, _ := g.acquire(context.Background(), nil)
	g.finish(ticket, RateLimitError{})
	firstDeadline := g.until
	for i := 1; i < workerCount; i++ {
		g.finish(ticket, RateLimitError{})
	}
	g.finish(ticket, nil) // A previously in-flight success must not lift the pause.
	if g.delay != 2*time.Second || g.until != firstDeadline || !g.cooling {
		t.Fatal("concurrent rejections multiplied the delay or an old success cleared it", g)
	}
	for _, want := range []time.Duration{4 * time.Second, 8 * time.Second, 8 * time.Second} {
		g.until = time.Now().Add(-time.Second)
		probe, _ := g.acquire(context.Background(), nil)
		before := time.Now()
		g.finish(probe, RateLimitError{})
		if !probe.probe || g.delay != want || g.until.Before(before.Add(want)) || g.until.After(time.Now().Add(g.maximum)) {
			t.Fatal("incorrect exponential backoff", want, g)
		}
	}
	g.until = time.Now().Add(-time.Second)
	probe, _ := g.acquire(context.Background(), nil)
	before := time.Now()
	g.finish(ticket, RateLimitError{RetryAfter: time.Hour})
	g.finish(probe, nil)
	if !g.cooling || g.until.Before(before.Add(time.Hour)) {
		t.Fatal("a late server deadline was capped or cleared by an older probe", g)
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, err := g.acquire(ctx, func(time.Time) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cooldown ignored cancellation", err)
	}
	g.until = time.Now().Add(-time.Second)
	probe, _ = g.acquire(context.Background(), nil)
	g.finish(probe, nil)
	if g.cooling || g.delay != 0 {
		t.Fatal("successful probe did not resume and reset", g)
	}
}

func TestOnlyOneWorkerProbesBeforeOthersResume(t *testing.T) {
	g := quickGate()
	ticket, _ := g.acquire(context.Background(), nil)
	g.finish(ticket, RateLimitError{})
	g.until = time.Now().Add(-time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	entered, release := make(chan struct{}, workerCount), make(chan struct{})
	waiting, done := make(chan struct{}, workerCount), make(chan error, workerCount)
	for i := 0; i < workerCount; i++ {
		go func() {
			done <- g.call(ctx, func(time.Time) { waiting <- struct{}{} }, func(ctx context.Context) error {
				entered <- struct{}{}
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
		}()
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("probe never started")
	}
	for i := 1; i < workerCount; i++ {
		select {
		case <-waiting:
		case <-ctx.Done():
			t.Fatal("other workers did not wait")
		}
	}
	if len(entered) != 0 {
		t.Fatal("multiple workers probed together")
	}
	close(release)
	for i := 0; i < workerCount; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if len(entered) != workerCount-1 {
		t.Fatal("workers did not resume")
	}
}

func TestRateLimitsRetryCheckAndSameWebPassWithoutLosingCheckpoint(t *testing.T) {
	s := sampleStore(t, true)
	var mu sync.Mutex
	var checks int
	storefrontCalls, webCalls := map[storefront.Source]int{}, map[storefront.Source]int{}
	bodies := map[storefront.Source]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if req.URL.Path == "/models" {
			checks++
			if checks == 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			fmt.Fprint(w, `{"models":[{"slug":"gpt-6.1-terra"}]}`)
			return
		}
		body, _ := io.ReadAll(req.Body)
		source := storefront.DTRPG
		if strings.Contains(string(body), "dmsguild.com") {
			source = storefront.DMSGuild
		}
		if !strings.Contains(string(body), `"tools"`) {
			storefrontCalls[source]++
			result := complete()
			result.Levels = enrichdata.Range{Sources: []string{}}
			fmt.Fprint(w, terminal(result))
			return
		}
		webCalls[source]++
		if webCalls[source] == 1 {
			bodies[source] = string(body)
		} else if bodies[source] != string(body) {
			t.Error("retry changed the book, pass or source evidence")
		}
		b, err := s.BookEnrichment(context.Background(), source, 1)
		if err != nil || !b.Players.Complete() || b.Levels.Complete() {
			t.Error("retry lost or failed its checkpoint", b, err)
		}
		history, err := s.EnrichmentHistory(context.Background(), 0)
		if err != nil {
			t.Error(err)
		}
		for _, saved := range history {
			if saved.Source == source && (!saved.PlayersDone || saved.LevelsDone || saved.Status != "partial") {
				t.Error("retry changed checkpoint completion", saved)
			}
		}
		switch webCalls[source] {
		case 1:
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			fmt.Fprint(w, "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"subscription_sharing_usage_limit_exceeded\"}}}\n\n")
		default:
			fmt.Fprint(w, terminal(enrichdata.Result{Players: enrichdata.Range{Sources: []string{}}, Levels: enrichdata.Range{Sources: []string{}}}))
		}
	}))
	defer srv.Close()
	c := NewClient(func(context.Context) (string, error) { return "synthetic", nil })
	c.Base = srv.URL
	r := &Runner{newGate: quickGate}
	t.Cleanup(r.Shutdown)
	if err := r.Start(context.Background(), s, c); err != nil {
		t.Fatal(err)
	}
	wait(t, r)
	v := r.Snapshot()
	if v.State != "completed" || v.Processed != 2 || v.Enriched != 2 || v.Unknown != 2 || v.Failed != 0 || v.WebPasses != 2 {
		t.Fatal(v)
	}
	mu.Lock()
	if checks != 2 || storefrontCalls[storefront.DTRPG] != 1 || storefrontCalls[storefront.DMSGuild] != 1 || webCalls[storefront.DTRPG] != 3 || webCalls[storefront.DMSGuild] != 3 {
		t.Error(checks, storefrontCalls, webCalls)
	}
	mu.Unlock()
	counts, err := s.EnrichmentCounts(context.Background())
	if err != nil || counts.Pending != 0 || counts.Failed != 0 || counts.Unknown != 2 {
		t.Fatal(counts, err)
	}
	r.Start(context.Background(), s, c)
	wait(t, r)
	if r.Snapshot().Processed != 0 {
		t.Fatal("completed unknowns were repeated")
	}
}

func TestStopInterruptsLongRateLimitWaitWithoutRecordingIssues(t *testing.T) {
	s := sampleStore(t, true)
	r := &Runner{}
	t.Cleanup(r.Shutdown)
	var calls atomic.Int64
	r.Start(context.Background(), s, fakeProvider{func(context.Context, store.EnrichmentBook, string, bool) (enrichdata.Result, error) {
		calls.Add(1)
		return enrichdata.Result{}, RateLimitError{RetryAfter: time.Hour}
	}})
	deadline := time.After(3 * time.Second)
	for {
		v := r.Snapshot()
		if len(v.Active) == 2 && v.Active[0].Stage == "waiting" && v.Active[1].Stage == "waiting" {
			if !v.Running || v.Failed != 0 || v.Processed != 0 || !strings.Contains(v.Active[0].Message, "automatically") {
				t.Fatal(v)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("dashboard never reported the cooldown", v)
		case <-time.After(time.Millisecond):
		}
	}
	r.Stop()
	select {
	case <-r.done:
	case <-time.After(time.Second):
		t.Fatal("Stop waited for the retry deadline")
	}
	counts, err := s.EnrichmentCounts(context.Background())
	if err != nil || counts.Pending != 2 || counts.Failed != 0 || r.Snapshot().State != "stopped" || calls.Load() > 2 {
		t.Fatal(counts, err, r.Snapshot(), calls.Load())
	}
	r.Start(context.Background(), s, fakeProvider{func(context.Context, store.EnrichmentBook, string, bool) (enrichdata.Result, error) {
		return complete(), nil
	}})
	wait(t, r)
	if r.Snapshot().Enriched != 2 {
		t.Fatal("a new run retained the old cooldown", r.Snapshot())
	}
}
