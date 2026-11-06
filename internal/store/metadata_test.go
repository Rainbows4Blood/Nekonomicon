package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/Rainbows4Blood/Nekonomicon/internal/metadata"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

func TestInspectDoesNotInitializeOrUpgrade(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := InspectContext(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Version() != 0 {
		t.Fatal("unexpected schema")
	}
	if _, err = os.Stat(filepath.Join(dir, "catalog.sqlite")); !os.IsNotExist(err) {
		t.Fatal("opening created a database", err)
	}
	if err = s.Upgrade(ctx, nil); err != nil {
		t.Fatal(err)
	}
	s.Close()
	// A v2 catalog remains v2, with no new table, after inspection.
	db, err := sql.Open("sqlite", filepath.Join(dir, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DROP VIEW current_product_metadata; DROP TABLE metadata_state; PRAGMA user_version=2;`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err = InspectContext(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var tables int
	s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='metadata_state'`).Scan(&tables)
	if s.Version() != 2 || tables != 0 {
		t.Fatal("inspection migrated schema")
	}
	if err = s.CreateRun(Run{ID: "no", Mode: "new"}); err == nil {
		t.Fatal("crawl accepted incompatible schema")
	}
	if err = s.Upgrade(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if s.Version() != SchemaVersion {
		t.Fatal("explicit upgrade did not finish")
	}
}
func TestMetadataCrawlAtomicityFailureAndFreshness(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	r := runTest(t, s, "crawl")
	body := []byte(` {"productId":42,"description":{"name":"Good"},"price":1.23456789,"authors":["A","B"]} `)
	if _, err := s.Commit(ctx, r.ID, []Result{{ID: 42, JSON: body}}); err != nil {
		t.Fatal(err)
	}
	var name, price string
	if err := s.db.QueryRow(`SELECT name,price FROM current_product_metadata WHERE product_id=42`).Scan(&name, &price); err != nil || name != "Good" || price != "1.23456789" {
		t.Fatal(name, price, err)
	}
	// A derived-write failure must roll back JSON, children and marker together.
	if _, err := s.db.Exec(`CREATE TRIGGER fail_metadata BEFORE INSERT ON product_creators BEGIN SELECT RAISE(ABORT,'synthetic'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(ctx, r.ID, []Result{{ID: 42, JSON: []byte(`{"productId":42,"authors":["Replacement"]}`)}}); err == nil {
		t.Fatal("derived storage failure ignored")
	}
	got, _ := s.Response(42)
	if string(got) != string(body) {
		t.Fatal("raw update escaped rollback")
	}
	s.db.Exec(`DROP TRIGGER fail_metadata`)
	// Complete extraction failure saves the raw response and preserves old
	// derived data, but excludes that stale data from the current-only view.
	v, err := s.Commit(ctx, r.ID, []Result{{ID: 42, JSON: []byte(`{"productId":99}`)}})
	if err != nil || v.MetadataFailures != 1 {
		t.Fatal(v, err)
	}
	var count int
	s.db.QueryRow(`SELECT count(*) FROM current_product_metadata WHERE product_id=42`).Scan(&count)
	if count != 0 {
		t.Fatal("stale metadata appeared current")
	}
	if err = s.db.QueryRow(`SELECT name FROM product_metadata WHERE product_id=42`).Scan(&name); err != nil || name != "Good" {
		t.Fatal("last good metadata lost", err)
	}
	if _, err = s.Commit(ctx, r.ID, []Result{{ID: 42, JSON: body}}); err != nil {
		t.Fatal(err)
	}
	var state string
	s.db.QueryRow(`SELECT status FROM metadata_state WHERE product_id=42`).Scan(&state)
	if state != "complete" {
		t.Fatal(state)
	}
}
func TestMetadataMaintenanceCursorRollbackResumeAndSourceIsolation(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	r := runTest(t, s, "seed")
	for _, id := range []int64{1, 2, 3} {
		if _, err := s.db.Exec(`INSERT INTO products VALUES('dtrpg',?,'original','seed',json_object('productId',?,'description',json_object('name','Book'),'pagecount','iv + 2'))`, id, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.BeginMetadata(ctx, storefront.DTRPG); err == nil {
		t.Fatal("maintenance accepted unfinished crawl")
	}
	s.UpdateRun(r.ID, "fetch", "completed", true)
	j, err := s.BeginMetadata(ctx, storefront.DTRPG)
	if err != nil {
		t.Fatal(err)
	}
	if j.Total != 3 {
		t.Fatal(j)
	}
	if err = s.CreateRun(Run{ID: "blocked", Mode: "new"}); err == nil {
		t.Fatal("crawl accepted maintenance owner")
	}
	rows, err := s.MetadataPage(ctx, storefront.DTRPG, 0)
	if err != nil {
		t.Fatal(err)
	}
	j, err = s.CommitMetadata(ctx, j, rows[:1])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER fail_projection BEFORE INSERT ON product_metadata WHEN NEW.product_id=3 BEGIN SELECT RAISE(ABORT,'synthetic'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CommitMetadata(ctx, j, rows[1:]); err == nil {
		t.Fatal("partial metadata batch committed")
	}
	got, _ := s.MetadataJob(ctx, storefront.DTRPG)
	if got.LastID != 1 || got.Processed != 1 {
		t.Fatal("cursor escaped rollback", got)
	}
	var count int
	s.db.QueryRow(`SELECT count(*) FROM product_metadata WHERE product_id=2`).Scan(&count)
	if count != 0 {
		t.Fatal("partial derived data escaped rollback")
	}
	s.db.Exec(`DROP TRIGGER fail_projection`)
	s.SetMetadataState(ctx, storefront.DTRPG, "paused")
	dir := s.Dir
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.BeginMetadata(ctx, storefront.DMSGuild); err == nil {
		t.Fatal("maintenance switched stores mid-run")
	}
	j, err = s.BeginMetadata(ctx, storefront.DTRPG)
	if err != nil {
		t.Fatal(err)
	}
	rows, err = s.MetadataPage(ctx, storefront.DTRPG, j.LastID)
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	j, err = s.CommitMetadata(ctx, j, rows)
	if err != nil || j.Processed != 3 || j.Warned != 3 {
		t.Fatal(j, err)
	}
	s.SetMetadataState(ctx, storefront.DTRPG, "completed_warnings")
	v, err := s.MetadataSummary(ctx, storefront.DTRPG)
	if err != nil || v.Current != 3 || v.Pending != 0 || v.Warnings != 3 {
		t.Fatal(v, err)
	}
	// Older extraction versions are pending even when response bytes did not change.
	s.db.Exec(`UPDATE metadata_state SET version=? WHERE product_id=2`, metadata.Version-1)
	v, _ = s.MetadataSummary(ctx, storefront.DTRPG)
	if v.Pending != 1 {
		t.Fatal("obsolete transformation not eligible", v)
	}
	other, _ := s.MetadataSummary(ctx, storefront.DMSGuild)
	if other.Total != 0 {
		t.Fatal("source leakage", other)
	}
}
