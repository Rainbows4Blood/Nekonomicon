package store

import (
	"context"
	"strings"
	"testing"

	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

func TestStorefrontFilterWithSearchPagingAndCursorIsolation(t *testing.T) {
	s := openTest(t)
	seedBooks(t, s, 73)
	ctx := context.Background()
	for _, source := range []storefront.Source{storefront.DTRPG, storefront.DMSGuild} {
		for _, sort := range []string{"name-asc", "name-desc", "date-asc", "date-desc"} {
			for _, search := range []string{"", "sapphire"} {
				q := defaultBookQuery()
				q.Source, q.Sort, q.Search, q.SearchIn, q.Edition = source, sort, search, "all", "dnd-35"
				seen := map[int64]bool{}
				for {
					p, err := s.Books(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					for _, b := range p.Books {
						if b.Source != source || seen[b.ID] {
							t.Fatal("wrong storefront or duplicate", b)
						}
						seen[b.ID] = true
					}
					if p.Next == "" {
						break
					}
					q.After = p.Next
				}
				if len(seen) != 73 {
					t.Fatal("missing books", source, sort, search, len(seen))
				}
			}
		}
	}
	q := defaultBookQuery()
	q.Source = storefront.DTRPG
	p, err := s.Books(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	q.After, q.Source = p.Next, storefront.DMSGuild
	if _, err = s.Books(ctx, q); err == nil {
		t.Fatal("cursor accepted from different source")
	}
	q = defaultBookQuery()
	q.Source = "invalid"
	if _, err = s.Books(ctx, q); err == nil {
		t.Fatal("invalid source accepted")
	}
}

func TestV5IndexUpgradePreservesBooksAndUsesCoveringCounts(t *testing.T) {
	s := openTest(t)
	seedBooks(t, s, 3)
	ctx := context.Background()
	for _, name := range []string{"dnd_source_name", "dnd_source_official_name", "dnd_source_date_asc", "dnd_source_date_desc", "dnd_source_official_date_asc", "dnd_source_official_date_desc", "products_freshness"} {
		if _, err := s.db.Exec("DROP INDEX " + name); err != nil {
			t.Fatal(err)
		}
	}
	s.db.Exec("PRAGMA user_version=5")
	dir := s.Dir
	s.Close()
	s, err := InspectContext(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Version() != 5 {
		t.Fatal("automatic upgrade")
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	if err = s.Upgrade(cancelCtx, func(message string) {
		if strings.Contains(message, "storefront") {
			cancel()
		}
	}); err == nil || s.Version() != 5 {
		t.Fatal("cancelled update committed", err)
	}
	if err = s.Upgrade(ctx, nil); err != nil {
		t.Fatal(err)
	}
	q := defaultBookQuery()
	q.Source = storefront.DTRPG
	p, err := s.Books(ctx, q)
	if err != nil || len(p.Books) != 3 {
		t.Fatal("existing index changed", err, p)
	}
	for _, table := range []string{"metadata_state", "group_state", "catalog_state"} {
		rows, err := s.db.Query("EXPLAIN QUERY PLAN SELECT count(*),sum(m.version=1 AND m.response_fetched_at=p.fetched_at) FROM products p INDEXED BY products_freshness LEFT JOIN " + table + " m USING(source,product_id)")
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var a, b, c int
			var detail string
			rows.Scan(&a, &b, &c, &detail)
			plan.WriteString(detail)
		}
		rows.Close()
		if !strings.Contains(plan.String(), "COVERING INDEX products_freshness") {
			t.Fatal(plan.String())
		}
	}
}
