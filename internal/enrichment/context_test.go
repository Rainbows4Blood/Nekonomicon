package enrichment

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/Rainbows4Blood/Nekonomicon/internal/enrichdata"
	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
)

func TestNewContextEnrichmentsPreserveFinishedRangesAndCheckpoints(t *testing.T) {
	s := sampleStore(t, true)
	ctx := context.Background()
	// Simulate v8's completed ranges (including unknown players).
	for _, source := range []string{"dmsguild", "dtrpg"} {
		b, err := s.NextEnrichment(ctx, "", 0)
		if source == "dtrpg" {
			b, err = s.NextEnrichment(ctx, "dmsguild", 1)
		}
		if err != nil || b == nil {
			t.Fatal(b, err)
		}
		b.PlayersDone, b.LevelsDone = true, true
		if err = s.SaveEnrichment(ctx, *b, enrichdata.Result{Levels: complete().Levels}, "old-cover", ""); err != nil {
			t.Fatal(err)
		}
	}
	r := &Runner{}
	t.Cleanup(r.Shutdown)
	var first, webCalls atomic.Int64
	r.Start(ctx, s, fakeProvider{func(_ context.Context, b store.EnrichmentBook, _ string, web bool) (enrichdata.Result, error) {
		if !b.PlayersDone || !b.LevelsDone {
			t.Error("old completions lost")
		}
		if web {
			webCalls.Add(1)
			if !b.SettingsDone || !b.LocationsDone || b.DatesDone || !b.Existing.Settings.Agnostic {
				t.Error("source-first markers lost", b)
			}
			return enrichdata.Result{}, errors.New("synthetic interrupted date lookup")
		}
		first.Add(1)
		v := complete()
		v.Settings.Agnostic = true
		v.Settings.Evidence = "Set in Forgotten Realms; adaptable to any campaign setting"
		v.Dates = enrichdata.Dates{}
		return v, nil
	}})
	wait(t, r)
	if first.Load() != 2 || webCalls.Load() != 2 || r.Snapshot().Failed != 2 {
		t.Fatal(first.Load(), webCalls.Load(), r.Snapshot())
	}
	c, err := s.EnrichmentCounts(ctx)
	if err != nil || c.Players != 2 || c.Levels != 2 || c.Settings != 2 || c.Locations != 2 || c.Dates != 0 || c.Pending != 2 {
		t.Fatal(c, err)
	}
	// Retry must keep the named setting + agnostic flag, and the unknown players,
	// even if the provider returns conflicting data in finished enrichments.
	r.Start(ctx, s, fakeProvider{func(_ context.Context, b store.EnrichmentBook, _ string, web bool) (enrichdata.Result, error) {
		if !b.SettingsDone || !b.LocationsDone || !b.PlayersDone || b.DatesDone {
			t.Error("retry lost finished markers", b)
		}
		v := complete()
		v.Settings.Names = []string{"Eberron"}
		v.Dates = enrichdata.Dates{}
		return v, nil
	}})
	wait(t, r)
	history, err := s.EnrichmentHistory(ctx, 0)
	if err != nil || len(history) != 2 {
		t.Fatal(history, err)
	}
	for _, b := range history {
		if !b.Done() || b.Status != "complete" || b.Existing.Players.Min != nil || b.Existing.Settings.Names[0] != "Forgotten Realms" || !b.Existing.Settings.Agnostic || b.Existing.Dates.Known() {
			t.Fatal(b)
		}
		d, err := s.BookDetails(ctx, b.Source, b.ID)
		if err != nil || !d.SettingsEnriched || !d.LocationsEnriched || !d.DatesEnriched || !d.Enrichment.Settings.Agnostic {
			t.Fatal(d, err)
		}
	}
	c, err = s.EnrichmentCounts(ctx)
	if err != nil || c.Pending != 0 || c.Dates != 2 || c.Unknown != 2 || c.Failed != 0 {
		t.Fatal(c, err)
	}
	r.Start(ctx, s, fakeProvider{func(context.Context, store.EnrichmentBook, string, bool) (enrichdata.Result, error) {
		t.Error("finished unknowns repeated")
		return enrichdata.Result{}, nil
	}})
	wait(t, r)
}
