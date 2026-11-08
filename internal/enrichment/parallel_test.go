package enrichment

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Rainbows4Blood/Nekonomicon/internal/enrichdata"
	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

func TestSuccessfulUnknownIsEnrichedAndSkipped(t *testing.T) {
	for _, kind := range []string{"all unknown", "players unknown", "levels unknown", "partial range"} {
		t.Run(kind, func(t *testing.T) {
			s := sampleStore(t, true)
			var calls atomic.Int64
			p := fakeProvider{func(_ context.Context, _ store.EnrichmentBook, _ string, _ bool) (enrichdata.Result, error) {
				calls.Add(1)
				v := complete()
				switch kind {
				case "all unknown":
					v = enrichdata.Result{}
				case "players unknown":
					v.Players = enrichdata.Range{}
				case "levels unknown":
					v.Levels = enrichdata.Range{}
				case "partial range":
					v.Players.Max = nil
				}
				return v, nil
			}}
			r := &Runner{}
			r.Start(context.Background(), s, p)
			wait(t, r)
			v := r.Snapshot()
			if v.Enriched != 2 || v.Unknown != 2 || v.Failed != 0 || calls.Load() != 4 {
				t.Fatal(v, calls.Load())
			}
			c, err := s.EnrichmentCounts(context.Background())
			if err != nil || c.Pending != 0 || c.Players != 2 || c.Levels != 2 || c.Unknown != 2 || c.Failed != 0 {
				t.Fatal(c, err)
			}
			history, err := s.EnrichmentHistory(context.Background(), 0)
			if err != nil || len(history) != 2 {
				t.Fatal(history, err)
			}
			for _, b := range history {
				if b.Status != "complete" || !b.PlayersDone || !b.LevelsDone {
					t.Fatal(b)
				}
			}
			r.Start(context.Background(), s, p)
			wait(t, r)
			if calls.Load() != 4 {
				t.Fatal("successful unknown results were sent again")
			}
		})
	}
}

func TestBoundedParallelCompletionAndResumeOutOfOrder(t *testing.T) {
	s := sampleCatalog(t, true, workerCount+1) // Two pools' worth of adventures plus two non-adventures.
	ctx := context.Background()
	entered := make(chan struct{}, 2*workerCount)
	release := make(chan struct{})
	var active, maximum atomic.Int64
	r := &Runner{}
	t.Cleanup(r.Shutdown)
	p := fakeProvider{func(ctx context.Context, b store.EnrichmentBook, _ string, _ bool) (enrichdata.Result, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old; old = maximum.Load() {
			if maximum.CompareAndSwap(old, n) {
				break
			}
		}
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return enrichdata.Result{}, ctx.Err()
		}
		// Hold the first key until Stop, while later keys finish and commit.
		if b.Source == storefront.DMSGuild && b.ID == 1 {
			<-ctx.Done()
			return enrichdata.Result{}, ctx.Err()
		}
		return complete(), nil
	}}
	r.Start(ctx, s, p)
	for i := 0; i < workerCount; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("requests did not run concurrently")
		}
	}
	v := r.Snapshot()
	if len(v.Active) != workerCount {
		t.Fatal(v)
	}
	v.Active[0].Name = "mutated snapshot"
	if r.Snapshot().Active[0].Name == "mutated snapshot" {
		t.Fatal("snapshot aliases worker state")
	}
	close(release)
	deadline := time.After(5 * time.Second)
	for r.Snapshot().Processed != 2*workerCount-1 {
		select {
		case <-deadline:
			t.Fatal("later books did not finish", r.Snapshot())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if maximum.Load() != workerCount {
		t.Fatal("concurrency limit violated", maximum.Load())
	}
	r.Stop()
	wait(t, r)
	c, err := s.EnrichmentCounts(ctx)
	if err != nil || c.Pending != 1 || r.Snapshot().State != "stopped" {
		t.Fatal(c, err, r.Snapshot())
	}
	var retried atomic.Int64
	r.Start(ctx, s, fakeProvider{func(_ context.Context, b store.EnrichmentBook, _ string, _ bool) (enrichdata.Result, error) {
		if b.Source != storefront.DMSGuild || b.ID != 1 {
			t.Error("completed book repeated", b.Source, b.ID)
		}
		retried.Add(1)
		return complete(), nil
	}})
	wait(t, r)
	if retried.Load() != 1 || r.Snapshot().Enriched != 1 {
		t.Fatal(retried.Load(), r.Snapshot())
	}
}

func TestCompletedUnknownRangePreservedWhileOtherRangeRetries(t *testing.T) {
	s := sampleStore(t, true)
	ctx := context.Background()
	b, err := s.NextEnrichment(ctx, "", 0)
	if err != nil || b == nil {
		t.Fatal(b, err)
	}
	b.PlayersDone = true
	if err = s.SaveEnrichment(ctx, *b, enrichdata.Result{}, "hash", ""); err != nil {
		t.Fatal(err)
	}
	r := &Runner{}
	r.Start(ctx, s, fakeProvider{func(_ context.Context, book store.EnrichmentBook, _ string, _ bool) (enrichdata.Result, error) {
		if book.Source == b.Source && !book.PlayersDone {
			t.Error("missing completed-unknown marker")
		}
		return complete(), nil
	}})
	wait(t, r)
	result, err := s.BookEnrichment(ctx, b.Source, b.ID)
	if err != nil || result.Players.Min != nil || result.Players.Max != nil || !result.Levels.Complete() {
		t.Fatal(result, err)
	}
	if r.Snapshot().Enriched != 2 || r.Snapshot().Unknown != 1 {
		t.Fatal(r.Snapshot())
	}
}

func TestFailedFallbackRemainsRetryable(t *testing.T) {
	s := sampleStore(t, true)
	r := &Runner{}
	r.Start(context.Background(), s, fakeProvider{func(_ context.Context, _ store.EnrichmentBook, _ string, web bool) (enrichdata.Result, error) {
		if web {
			return enrichdata.Result{}, errors.New("Interrupted response")
		}
		v := complete()
		v.Levels = enrichdata.Range{}
		return v, nil
	}})
	wait(t, r)
	c, err := s.EnrichmentCounts(context.Background())
	if err != nil || c.Pending != 2 || c.Failed != 2 || c.Players != 2 || c.Levels != 0 || c.Unknown != 0 {
		t.Fatal(c, err)
	}
	if r.Snapshot().Enriched != 0 {
		t.Fatal("failure counted as enriched")
	}
	r.Start(context.Background(), s, fakeProvider{func(_ context.Context, b store.EnrichmentBook, _ string, _ bool) (enrichdata.Result, error) {
		if !b.PlayersDone || b.LevelsDone {
			t.Error("checkpoint markers lost")
		}
		return enrichdata.Result{}, nil
	}})
	wait(t, r)
	c, err = s.EnrichmentCounts(context.Background())
	if err != nil || c.Pending != 0 || c.Failed != 0 || c.Unknown != 2 || r.Snapshot().Enriched != 2 {
		t.Fatal(c, err, r.Snapshot())
	}
}

func TestGlobalFailureCancelsOtherRequestsAndRetainsPending(t *testing.T) {
	s := sampleCatalog(t, true, workerCount+1)
	r := &Runner{}
	t.Cleanup(r.Shutdown)
	entered := make(chan struct{}, workerCount)
	release := make(chan struct{})
	var calls, cancelled atomic.Int64
	r.Start(context.Background(), s, fakeProvider{func(ctx context.Context, b store.EnrichmentBook, _ string, _ bool) (enrichdata.Result, error) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return enrichdata.Result{}, ctx.Err()
		}
		if b.Source == storefront.DMSGuild && b.ID == 1 {
			return enrichdata.Result{}, StopError{"OpenAI access denied"}
		}
		<-ctx.Done()
		cancelled.Add(1)
		return enrichdata.Result{}, ctx.Err()
	}})
	for i := 0; i < workerCount; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("requests did not start")
		}
	}
	close(release)
	wait(t, r)
	if calls.Load() != workerCount || cancelled.Load() != workerCount-1 || r.Snapshot().State != "blocked" {
		t.Fatal(calls.Load(), cancelled.Load(), r.Snapshot())
	}
	c, err := s.EnrichmentCounts(context.Background())
	if err != nil || c.Pending != 2*workerCount || c.Failed != 1 {
		t.Fatal(c, err)
	}
}
