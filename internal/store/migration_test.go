package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func legacyCatalog(t *testing.T, large bool) string {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema, err := os.ReadFile("testdata/v1.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO runs VALUES('old','refresh','fetch','paused','now',NULL,1);
INSERT INTO products VALUES('dtrpg',42,'now','old',' {"productId":42,"included":[]} ');
INSERT INTO covers VALUES('dtrpg',42,'image','covers/dtrpg/42/image.png','now');`); err != nil {
		t.Fatal(err)
	}
	if large {
		body := `{"body":"` + strings.Repeat("x", 8192) + `"}`
		if _, err = db.Exec(`WITH RECURSIVE ids(n) AS (SELECT 100 UNION ALL SELECT n+1 FROM ids WHERE n<2099)
INSERT INTO products SELECT 'dtrpg',n,'now','old',? FROM ids`, body); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCancelledMigrationRollsBackAndReleasesWriter(t *testing.T) {
	for _, stage := range []string{"Preparing your catalog upgrade…", "Preserving recorded errors…", "Finishing your catalog upgrade…", "during product copy"} {
		t.Run(stage, func(t *testing.T) {
			dir := legacyCatalog(t, stage == "during product copy")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reached := false
			s, err := OpenContext(ctx, dir, func(message string) {
				if message == stage {
					reached = true
					cancel()
				}
				if stage == "during product copy" && message == "Copying saved products…" {
					reached = true
					time.AfterFunc(time.Millisecond, cancel)
				}
			})
			if s != nil {
				s.Close()
				t.Fatal("cancelled catalog opened")
			}
			if !reached || !errors.Is(err, context.Canceled) {
				t.Fatal("migration ignored cancellation", err)
			}
			db, err := sql.Open("sqlite", filepath.Join(dir, "catalog.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			var version, columns, temporary int
			if err = db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow(`SELECT count(*) FROM pragma_table_info('runs')`).Scan(&columns); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('products_v1','covers_v1','failures_v1')`).Scan(&temporary); err != nil {
				t.Fatal(err)
			}
			db.Close()
			if version != 1 || columns != 7 || temporary != 0 {
				t.Fatalf("partial migration: version=%d columns=%d temporary=%d", version, columns, temporary)
			}
			// Reopening also proves the cancelled attempt released its writer lock.
			s, err = Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			b, err := s.Response(42)
			if err != nil || string(b) != ` {"productId":42,"included":[]} ` {
				t.Fatal("lost response bytes", err)
			}
			page, err := s.CoverPage(0)
			if err != nil || page[0].Path != "covers/dtrpg/42/image.png" {
				t.Fatal("lost cover reference", err)
			}
		})
	}
}
