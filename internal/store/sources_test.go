package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

func TestStorefrontIsolationAndRecovery(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, source := range []storefront.Source{storefront.DTRPG, storefront.DMSGuild} {
		r := Run{ID: string(source), Source: source, Mode: "refresh", Phase: "fetch", State: "paused", Started: now()}
		if err = s.CreateRun(r); err != nil {
			t.Fatal(err)
		}
		batch := []Result{{ID: 42, JSON: []byte(`{"store":"` + string(source) + `","included":[]}`)}, {ID: 43, Failure: &Failure{Phase: "product", Kind: "restricted", Message: "synthetic"}}, {ID: 42, Cover: &Cover{URL: string(source), Path: "covers/" + string(source) + "/42/image.png"}}}
		if _, err = s.Commit(ctx, r.ID, batch); err != nil {
			t.Fatal(err)
		}
		if source == storefront.DTRPG {
			if err = s.UpdateRun(r.ID, "fetch", "completed_errors", true); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, err := s.Active()
	if err != nil || r.Source != storefront.DMSGuild {
		t.Fatal("run lost its storefront", err)
	}
	if err = s.CreateRun(Run{ID: "concurrent", Source: storefront.DTRPG, Mode: "new"}); err == nil {
		t.Fatal("allowed a second active run")
	}
	for _, source := range []storefront.Source{storefront.DTRPG, storefront.DMSGuild} {
		stats, err := s.Stats(source)
		if err != nil || stats.Products != 1 || stats.Covers != 1 || stats.ProductFailures != 1 {
			t.Fatal("mixed counts", stats, err)
		}
		b, err := s.Response(42, source)
		if err != nil || string(b) != `{"store":"`+string(source)+`","included":[]}` {
			t.Fatal("response overwritten across stores", err)
		}
		page, err := s.CoverPage(0, source)
		if err != nil || len(page) != 1 || page[0].URL != string(source) {
			t.Fatal("mixed cover page", err)
		}
		ids, err := s.Eligibility(Run{ID: "next", Source: source, Mode: "new"}, []int64{42, 43, 44})
		if err != nil || len(ids) != 2 || ids[0] != 43 {
			t.Fatal("mixed eligibility", ids, err)
		}
	}
	ids, err := s.Eligibility(*r, []int64{41, 42, 43, 44})
	if err != nil || len(ids) != 2 || ids[0] != 41 || ids[1] != 44 {
		t.Fatal("out of order recovery", ids, err)
	}
	if _, err = s.Commit(ctx, r.ID, []Result{{ID: 43, JSON: []byte(`{"productId":43}`)}}); err != nil {
		t.Fatal(err)
	}
	dm, _ := s.Failures(0, 50, storefront.DMSGuild)
	dt, _ := s.Failures(0, 50, storefront.DTRPG)
	if len(dm) != 0 || len(dt) != 1 {
		t.Fatal("success cleared the other storefront failure")
	}
}

func TestV1MigrationPreservesCatalogAndRollsBack(t *testing.T) {
	legacy, err := os.ReadFile("testdata/v1.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, blocked := range []bool{false, true} {
		dir := t.TempDir()
		db, err := sql.Open("sqlite", filepath.Join(dir, "catalog.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(string(legacy)); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO runs VALUES('old','refresh','fetch','paused','now',NULL,1);
INSERT INTO products VALUES('dtrpg',42,'now','old',' {"productId":42,"included":[]} ');
INSERT INTO covers VALUES('dtrpg',42,'image','covers/dtrpg/42/image.png','now');
INSERT INTO failures VALUES('dtrpg',43,'product','old','restricted',403,'synthetic','now');`); err != nil {
			t.Fatal(err)
		}
		if blocked {
			if _, err = db.Exec(`CREATE TABLE products_v1(dummy)`); err != nil {
				t.Fatal(err)
			}
		}
		db.Close()
		s, err := Open(dir)
		if blocked {
			if err == nil {
				s.Close()
				t.Fatal("expected migration failure")
			}
			db, err = sql.Open("sqlite", filepath.Join(dir, "catalog.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			var version, columns int
			db.QueryRow(`PRAGMA user_version`).Scan(&version)
			db.QueryRow(`SELECT count(*) FROM pragma_table_info('runs')`).Scan(&columns)
			if version != 1 || columns != 7 {
				t.Fatal("partial migration escaped rollback")
			}
			if _, err = db.Exec(`DROP TABLE products_v1`); err != nil {
				t.Fatal(err)
			}
			db.Close()
			s, err = Open(dir)
		}
		if err != nil {
			t.Fatal(err)
		}
		r, err := s.Active()
		if err != nil || r.Source != storefront.DTRPG || r.ID != "old" {
			t.Fatal("lost legacy run", err)
		}
		b, err := s.Response(42)
		if err != nil || string(b) != ` {"productId":42,"included":[]} ` {
			t.Fatal("migration changed bytes", err)
		}
		stats, err := s.Stats()
		if err != nil || stats.Products != 1 || stats.Covers != 1 || stats.ProductFailures != 1 {
			t.Fatal("migration lost rows", err)
		}
		s.Close()
	}
}
