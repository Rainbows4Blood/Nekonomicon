package store

import (
	"context"
	"testing"

	"github.com/Rainbows4Blood/Nekonomicon/internal/catalog"
	"github.com/Rainbows4Blood/Nekonomicon/internal/enrichdata"
	"github.com/Rainbows4Blood/Nekonomicon/internal/metadata"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

func TestAdventureEligibilityUsesSourceTaxonomy(t *testing.T) {
	for _, tt := range []struct {
		source storefront.Source
		raw    string
		want   bool
	}{
		{storefront.DTRPG, `{"productId":1,"filters":[{"filterId":2110,"parentId":2150,"groupId":1}]}`, true},
		{storefront.DMSGuild, `{"productId":1,"filters":[{"filterId":45393,"parentId":45341,"groupId":29}]}`, true},
		{storefront.DTRPG, `{"productId":1,"filters":[{"filterId":2110,"parentId":999}]}`, false},
		{storefront.DTRPG, `{"productId":1,"description":{"name":"Adventures"},"categories":[{"categoryId":55,"descriptions":[{"name":"Adventures"}]}]}`, false},
		{storefront.DTRPG, `{"productId":1,"filters":[{"filterId":55,"descriptions":[{"name":"Adventures"}]}]}`, true},
	} {
		if got := catalog.IsAdventure(tt.source, metadata.Extract([]byte(tt.raw), 1)); got != tt.want {
			t.Fatalf("eligibility=%v want=%v", got, tt.want)
		}
	}
}
func TestEnrichmentStaleDataRollbackAndRestart(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.CreateRun(Run{ID: "seed", Mode: "new"}); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"productId":1,"description":{"name":"Adventure"},"filters":[{"filterId":2110},{"filterId":44705}]}`)
	if _, err := s.Commit(ctx, "seed", []Result{{ID: 1, JSON: raw}}); err != nil {
		t.Fatal(err)
	}
	s.UpdateRun("seed", "fetch", "completed", true)
	b, err := s.NextEnrichment(ctx, "", 0)
	if err != nil || b == nil {
		t.Fatal(b, err)
	}
	min, max := 3, 6
	r := enrichdata.Result{Players: enrichdata.Range{Min: &min, Max: &max, Evidence: "Source says 3–6"}}
	if err = s.SaveEnrichment(ctx, *b, r, "hash", ""); err != nil {
		t.Fatal(err)
	}
	var stored int
	if err = s.db.QueryRow("SELECT min_player FROM enrichment_results").Scan(&stored); err != nil || stored != 3 {
		t.Fatal(stored, err)
	}
	s.db.Exec(`CREATE TRIGGER fail_enrichment BEFORE UPDATE ON enrichment_results BEGIN SELECT RAISE(ABORT,'synthetic'); END`)
	r.Levels = r.Players
	if err = s.SaveEnrichment(ctx, *b, r, "hash", ""); err == nil {
		t.Fatal("failed update succeeded")
	}
	s.db.Exec("DROP TRIGGER fail_enrichment")
	got, err := s.BookEnrichment(ctx, storefront.DTRPG, 1)
	if err != nil || got.Levels.Complete() {
		t.Fatal("failed write changed data", err)
	}
	// Projection maintenance replaces book rows, but retains enrichment separately.
	s.db.Exec("UPDATE catalog_state SET version=0")
	jobs, err := s.BeginDatabaseMaintenance(ctx, "catalog")
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		rows, e := s.CatalogPage(ctx, j.Source, 0)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.CommitCatalog(ctx, j, rows); e != nil {
			t.Fatal(e)
		}
	}
	s.SetDatabaseMaintenanceState(ctx, "catalog", "completed")
	got, err = s.BookEnrichment(ctx, storefront.DTRPG, 1)
	if err != nil || !got.Players.Complete() {
		t.Fatal("maintenance lost enrichment", err)
	}
	dir := s.Dir
	s.Close()
	s, err = InspectContext(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err = s.NextEnrichment(ctx, "", 0)
	if err != nil || !b.Existing.Players.Complete() {
		t.Fatal("restart lost partial result", err)
	}
	s.db.Exec("UPDATE products SET fetched_at='changed'")
	if err = s.SaveEnrichment(ctx, *b, r, "hash", ""); err == nil {
		t.Fatal("stale result committed")
	}
	got, err = s.BookEnrichment(ctx, storefront.DTRPG, 1)
	if err != nil || got.Players.Min != nil {
		t.Fatal("stale result displayed", err)
	}
}
