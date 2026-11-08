package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/Rainbows4Blood/Nekonomicon/internal/enrichdata"
)

func TestContextUpgradePreservesV8CompletedUnknownsAndRollsBack(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.CreateRun(Run{ID: "seed", Mode: "new"}); err != nil {
		t.Fatal(err)
	}
	for id := int64(1); id <= 2; id++ {
		raw := []byte(fmt.Sprintf(`{"productId":%d,"filters":[{"filterId":2110},{"filterId":44705}]}`, id))
		if _, err := s.Commit(ctx, "seed", []Result{{ID: id, JSON: raw}}); err != nil {
			t.Fatal(err)
		}
	}
	s.UpdateRun("seed", "fetch", "completed", true)
	for id := int64(1); id <= 2; id++ {
		b, err := s.NextEnrichment(ctx, "", 0)
		if err != nil || b == nil {
			t.Fatal(b, err)
		}
		b.PlayersDone, b.LevelsDone, b.SettingsDone, b.LocationsDone, b.DatesDone = true, true, true, true, true
		v := enrichdata.Result{}
		if id == 1 {
			n := 4
			v.Players = enrichdata.Range{Min: &n, Max: &n, Evidence: "Four players"}
		}
		if err = s.SaveEnrichment(ctx, *b, v, "hash", ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"settings", "locations", "dates"} {
		for _, suffix := range []string{"json", "done", "complete"} {
			if _, err := s.db.Exec("ALTER TABLE enrichment_results DROP COLUMN " + name + "_" + suffix); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := s.db.Exec("PRAGMA user_version=8"); err != nil {
		t.Fatal(err)
	}
	dir := s.Dir
	s.Close()
	var err error
	s, err = InspectContext(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	if s.Version() != 8 {
		t.Fatal("opening migrated catalog")
	}
	if err = s.CreateRun(Run{ID: "blocked", Mode: "new"}); err == nil {
		t.Fatal("crawl permitted before explicit update")
	}
	// Failure after adding columns must roll the whole update back.
	if _, err = s.db.Exec(`CREATE TRIGGER fail_context BEFORE UPDATE ON enrichment_results BEGIN SELECT RAISE(ABORT,'synthetic'); END`); err != nil {
		t.Fatal(err)
	}
	if err = s.Upgrade(ctx, nil); err == nil || s.Version() != 8 {
		t.Fatal("failed update committed", err)
	}
	var columns int
	if err = s.db.QueryRow(`SELECT count(*) FROM pragma_table_info('enrichment_results') WHERE name='settings_json'`).Scan(&columns); err != nil || columns != 0 {
		t.Fatal(columns, err)
	}
	s.db.Exec("DROP TRIGGER fail_context")
	cancelCtx, cancel := context.WithCancel(ctx)
	if err = s.Upgrade(cancelCtx, func(msg string) {
		if msg == "Adding setting, location and in-world date enrichments…" {
			cancel()
		}
	}); err == nil || s.Version() != 8 {
		t.Fatal("cancelled update committed", err)
	}
	if err = s.Upgrade(ctx, nil); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = InspectContext(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.EnrichmentCounts(ctx)
	if err != nil || c.Pending != 2 || c.Players != 2 || c.Levels != 2 || c.Settings != 0 || c.Locations != 0 || c.Dates != 0 {
		t.Fatal(c, err)
	}
	var after int64
	for id := int64(1); id <= 2; id++ {
		b, err := s.NextEnrichment(ctx, "dtrpg", after)
		if err != nil || b == nil || b.ID != id {
			t.Fatal(b, err)
		}
		after = b.ID
		if !b.PlayersDone || !b.LevelsDone || b.SettingsDone || b.LocationsDone || b.DatesDone {
			t.Fatal(b)
		}
		if id == 1 && (b.Existing.Players.Min == nil || *b.Existing.Players.Min != 4) || id == 2 && b.Existing.Players.Min != nil {
			t.Fatal("legacy value changed", b)
		}
	}
}
