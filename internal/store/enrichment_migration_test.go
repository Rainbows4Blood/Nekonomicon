package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/Rainbows4Blood/Nekonomicon/internal/enrichdata"
)

func TestEnrichmentCompletionUpgradeAndUnknownRestart(t *testing.T) {
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
	b, err := s.NextEnrichment(ctx, "", 0)
	if err != nil || b == nil {
		t.Fatal(b, err)
	}
	min, max := 3, 5
	rangeValue := enrichdata.Range{Min: &min, Max: &max, Evidence: "For 3–5"}
	b.SettingsDone, b.LocationsDone, b.DatesDone = true, true, true
	if err = s.SaveEnrichment(ctx, *b, enrichdata.Result{Players: rangeValue, Levels: rangeValue}, "hash", ""); err != nil {
		t.Fatal(err)
	}
	b, err = s.NextEnrichment(ctx, "", 0)
	if err != nil || b == nil {
		t.Fatal(b, err)
	}
	if err = s.SaveEnrichment(ctx, *b, enrichdata.Result{}, "hash", ""); err != nil {
		t.Fatal(err)
	}
	// Recreate v7's ambiguous state: unknown could mean finished or a checkpoint.
	for _, q := range []string{"ALTER TABLE enrichment_results DROP COLUMN players_done", "ALTER TABLE enrichment_results DROP COLUMN levels_done", "PRAGMA user_version=7", `CREATE TRIGGER fail_completion BEFORE UPDATE ON enrichment_results BEGIN SELECT RAISE(ABORT,'synthetic'); END`} {
		if _, err = s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	dir := s.Dir
	s.Close()
	s, err = InspectContext(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	if s.Version() != 7 {
		t.Fatal("opening migrated the catalog")
	}
	if err = s.Upgrade(ctx, nil); err == nil || s.Version() != 7 {
		t.Fatal("failed upgrade committed", err)
	}
	var columns int
	if err = s.db.QueryRow(`SELECT count(*) FROM pragma_table_info('enrichment_results') WHERE name IN ('players_done','levels_done')`).Scan(&columns); err != nil || columns != 0 {
		t.Fatal("migration did not roll back", columns, err)
	}
	if _, err = s.db.Exec("DROP TRIGGER fail_completion"); err != nil {
		t.Fatal(err)
	}
	if err = s.Upgrade(ctx, nil); err != nil {
		t.Fatal(err)
	}
	c, err := s.EnrichmentCounts(ctx)
	if err != nil || c.Pending != 1 || c.Players != 1 || c.Levels != 1 {
		t.Fatal(c, err)
	}
	b, err = s.NextEnrichment(ctx, "", 0)
	if err != nil || b == nil || b.ID != 2 {
		t.Fatal(b, err)
	}
	b.PlayersDone, b.LevelsDone = true, true
	b.SettingsDone, b.LocationsDone, b.DatesDone = true, true, true
	if err = s.SaveEnrichment(ctx, *b, enrichdata.Result{}, "hash", ""); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = InspectContext(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.EnrichmentCounts(ctx)
	if err != nil || c.Pending != 0 || c.Players != 2 || c.Levels != 2 || c.Unknown != 2 || c.Failed != 0 {
		t.Fatal("unknown completion lost on restart", c, err)
	}
	details, err := s.BookDetails(ctx, b.Source, b.ID)
	if err != nil || !details.PlayersEnriched || !details.LevelsEnriched || details.Enrichment.Players.Min != nil {
		t.Fatal("unknown completion missing from details", details, err)
	}
}
