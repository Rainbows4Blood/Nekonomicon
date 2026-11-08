package store

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

func seedBooks(t *testing.T, s *Store, n int) {
	t.Helper()
	for _, source := range []storefront.Source{storefront.DTRPG, storefront.DMSGuild} {
		run := Run{ID: string(source), Source: source, Mode: "new", Phase: "fetch", State: "running"}
		if err := s.CreateRun(run); err != nil {
			t.Fatal(err)
		}
		var batch []Result
		for i := 1; i <= n; i++ {
			date := fmt.Sprintf("2020-%02d-%02d", 1+(i/28)%12, 1+i%28)
			if i%7 == 0 {
				date = ""
			}
			publisher := `,"publisherId":44`
			if i%3 == 0 {
				publisher = `,"publisherId":8957`
			} else if i%3 == 1 {
				publisher = ""
			}
			body := fmt.Sprintf(`{"productId":%d,"dateAvailable":%q,"description":{"name":"Tome %03d","description":"<p>A sapphire dragon guards the archive.</p>"},"filters":[{"filterId":44705},{"filterId":1000263}]%s}`, i, date, i%9, publisher)
			batch = append(batch, Result{ID: int64(i), JSON: []byte(body)})
		}
		if _, err := s.Commit(context.Background(), run.ID, batch); err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateRun(run.ID, "fetch", "completed", true); err != nil {
			t.Fatal(err)
		}
	}
}
func defaultBookQuery() BookQuery {
	return BookQuery{Official: "all", Sort: "name-asc", SearchIn: "name"}
}
func TestBooksFilteringSearchSortingAndKeysetPages(t *testing.T) {
	s := openTest(t)
	seedBooks(t, s, 73)
	ctx := context.Background()
	for _, sort := range []string{"name-asc", "name-desc", "date-asc", "date-desc"} {
		q := defaultBookQuery()
		q.Sort = sort
		var all []Book
		seen := map[string]bool{}
		for {
			page, err := s.Books(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for _, b := range page.Books {
				key := fmt.Sprint(b.Source, b.ID)
				if seen[key] {
					t.Fatal("duplicate across keyset pages", key)
				}
				seen[key] = true
				all = append(all, b)
			}
			if page.Next == "" {
				break
			}
			q.After = page.Next
		}
		if len(all) != 146 {
			t.Fatal(sort, len(all))
		}
		for i, b := range all {
			if i == 0 {
				continue
			}
			prev := all[i-1]
			if strings.HasPrefix(sort, "name") {
				if sort == "name-asc" && prev.Name > b.Name || sort == "name-desc" && prev.Name < b.Name {
					t.Fatal("name order", sort)
				}
			} else {
				if prev.ReleaseDate == "" && b.ReleaseDate != "" {
					t.Fatal("unknown dates were not last")
				}
				if b.ReleaseDate != "" && (sort == "date-asc" && prev.ReleaseDate > b.ReleaseDate || sort == "date-desc" && prev.ReleaseDate < b.ReleaseDate) {
					t.Fatal("date order", sort)
				}
			}
		}
	}
	q := defaultBookQuery()
	q.Search = "sapphire"
	page, err := s.Books(ctx, q)
	if err != nil || len(page.Books) != 0 {
		t.Fatal("description matched name-only search", err)
	}
	q.SearchIn = "all"
	page, err = s.Books(ctx, q)
	if err != nil || len(page.Books) != 50 || page.Next == "" {
		t.Fatal("FTS failed", err)
	}
	q.Search = "tome 001"
	q.Official = "official"
	q.Edition = "dnd-35"
	page, err = s.Books(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range page.Books {
		if b.Official == nil || !*b.Official || !strings.Contains(b.Name, "001") {
			t.Fatal(b)
		}
	}
	for _, flag := range []string{"official", "unofficial"} {
		q = defaultBookQuery()
		q.Official = flag
		page, err = s.Books(ctx, q)
		if err != nil || len(page.Books) == 0 {
			t.Fatal(flag, err)
		}
		for _, b := range page.Books {
			if b.Official == nil || *b.Official != (flag == "official") {
				t.Fatal("unknown leaked into official filter", b)
			}
		}
	}
	q = defaultBookQuery()
	q.Edition = "dnd-0"
	page, err = s.Books(ctx, q)
	if err != nil || len(page.Books) != 0 {
		t.Fatal("wrong edition", err)
	}
	q = defaultBookQuery()
	q.Search = `" OR * : )`
	if _, err = s.Books(ctx, q); err != nil {
		t.Fatal("search punctuation reached FTS grammar", err)
	}
	q = defaultBookQuery()
	first, _ := s.Books(ctx, q)
	q.After = first.Next
	q.Sort = "name-desc"
	if _, err = s.Books(ctx, q); err == nil {
		t.Fatal("cursor reused with different filters")
	}
}
func TestBooksLiveRefreshRollbackAndStaleExclusion(t *testing.T) {
	s := openTest(t)
	seedBooks(t, s, 2)
	ctx := context.Background()
	r := runTest(t, s, "refresh")
	body := []byte(`{"productId":2,"description":{"name":"Changed title","description":"<script>evil()</script><p>Moonstone &amp; magic</p>"},"publisherId":44,"filters":[{"filterId":44705}]}`)
	if _, err := s.db.Exec(`CREATE TRIGGER fail_catalog BEFORE INSERT ON dnd_listing BEGIN SELECT RAISE(ABORT,'synthetic');END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(ctx, r.ID, []Result{{ID: 2, JSON: body}}); err == nil {
		t.Fatal("index failure did not roll back live commit")
	}
	q := defaultBookQuery()
	q.Search = "Tome"
	page, err := s.Books(ctx, q)
	if err != nil || len(page.Books) != 4 {
		t.Fatal("old search rows lost", err)
	}
	s.db.Exec(`DROP TRIGGER fail_catalog`)
	if _, err = s.Commit(ctx, r.ID, []Result{{ID: 2, JSON: body}}); err != nil {
		t.Fatal(err)
	}
	q.Search = "moonstone"
	q.SearchIn = "all"
	page, err = s.Books(ctx, q)
	if err != nil || len(page.Books) != 1 {
		t.Fatal("new text not indexed", err)
	}
	d, err := s.BookDetails(ctx, storefront.DTRPG, 2)
	if err != nil || d.Name != "Changed title" || d.Description != "Moonstone & magic" {
		t.Fatal(d, err)
	}
	q.Search = "evil"
	page, _ = s.Books(ctx, q)
	if len(page.Books) != 0 {
		t.Fatal("script content indexed")
	}
	if _, err = s.Commit(ctx, r.ID, []Result{{ID: 2, JSON: []byte(`{"productId":999}`)}}); err != nil {
		t.Fatal(err)
	}
	q.Search = "moonstone"
	page, _ = s.Books(ctx, q)
	if len(page.Books) != 0 {
		t.Fatal("stale search result shown")
	}
	if _, err = s.BookDetails(ctx, storefront.DTRPG, 2); err == nil {
		t.Fatal("stale details shown")
	}
	if _, err = s.Commit(ctx, r.ID, []Result{{ID: 2, JSON: []byte(`{"productId":2,"description":{"name":"No D&D membership"}}`)}}); err != nil {
		t.Fatal(err)
	}
	q = defaultBookQuery()
	page, _ = s.Books(ctx, q)
	if len(page.Books) != 3 {
		t.Fatal("removed membership remained in browse", page)
	}
}
func TestBookQueryPlansUseIndexes(t *testing.T) {
	for _, source := range []storefront.Source{"", storefront.DTRPG, storefront.DMSGuild} {
		s := openTest(t)
		seedBooks(t, s, 3)
		for _, sort := range []string{"name-asc", "name-desc", "date-asc", "date-desc"} {
			for _, flag := range []string{"all", "official", "unofficial"} {
				q := defaultBookQuery()
				q.Sort, q.Official, q.Edition, q.Source = sort, flag, "dnd-35", source
				query, args, err := bookSelect(q)
				if err != nil {
					t.Fatal(err)
				}
				rows, err := s.db.Query("EXPLAIN QUERY PLAN "+query, args...)
				if err != nil {
					t.Fatal(err)
				}
				var plan strings.Builder
				for rows.Next() {
					var id, parent, unused int
					var detail string
					rows.Scan(&id, &parent, &unused, &detail)
					plan.WriteString(detail + "\n")
				}
				rows.Close()
				if !strings.Contains(strings.ReplaceAll(plan.String(), "COVERING ", ""), "USING INDEX dnd_") || strings.Contains(plan.String(), "TEMP B-TREE") || strings.Contains(plan.String(), "SCAN p") {
					t.Fatal(sort, flag, plan.String())
				}
			}
		}
		q := defaultBookQuery()
		q.Search = "dragon"
		q.SearchIn = "all"
		query, args, _ := bookSelect(q)
		rows, err := s.db.Query("EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var plan strings.Builder
		for rows.Next() {
			var a, b, c int
			var detail string
			rows.Scan(&a, &b, &c, &detail)
			plan.WriteString(detail + "\n")
		}
		if !strings.Contains(plan.String(), "dnd_search VIRTUAL TABLE INDEX") || strings.Contains(plan.String(), "SCAN p") {
			t.Fatal(plan.String())
		}
		t.Log(plan.String())
	}
}
func TestV4InspectionDoesNotBuildSearchIndex(t *testing.T) {
	s := openTest(t)
	seedBooks(t, s, 1)
	ctx := context.Background()
	s.db.Exec(`DROP TRIGGER dnd_search_insert;DROP TRIGGER dnd_search_delete;DROP TABLE dnd_listing;DROP TABLE dnd_search;DROP TABLE dnd_books;DROP TABLE catalog_jobs;DROP TABLE catalog_state;PRAGMA user_version=4`)
	dir := s.Dir
	s.Close()
	s, err := InspectContext(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Version() != 4 {
		t.Fatal("opening upgraded catalog")
	}
	if _, err = s.Books(ctx, defaultBookQuery()); err == nil {
		t.Fatal("browse before explicit schema update")
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	err = s.Upgrade(cancelCtx, func(message string) {
		if strings.Contains(message, "browsing") {
			cancel()
		}
	})
	if err == nil || s.Version() != 4 {
		t.Fatal("cancelled index schema update committed", err)
	}
	if err = s.Upgrade(ctx, nil); err != nil {
		t.Fatal(err)
	}
	summary, err := s.CatalogSummary(ctx, "")
	if err != nil || summary.Pending != 2 || summary.Current != 0 {
		t.Fatal("schema update silently backfilled index", summary, err)
	}
}
func BenchmarkBookQueries(b *testing.B) {
	s, err := Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.CreateRun(Run{ID: "bench", Mode: "new"}); err != nil {
		b.Fatal(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	insert, err := tx.Prepare(`INSERT INTO products VALUES(?,?,'fixture','bench',?)`)
	if err != nil {
		b.Fatal(err)
	}
	writer, err := prepareCatalog(ctx, tx)
	if err != nil {
		b.Fatal(err)
	}
	for i := 1; i <= 10000; i++ {
		source := storefront.DTRPG
		if i%2 == 0 {
			source = storefront.DMSGuild
		}
		text := strings.Repeat("A dragon guards the ancient archive. ", 50)
		if i%100 == 0 {
			text += " sapphire"
		}
		raw := []byte(fmt.Sprintf(`{"productId":%d,"dateAvailable":"2024-%02d-01","publisherId":44,"description":{"name":"Tome %05d","description":%q},"filters":[{"filterId":44705},{"filterId":1000263}]}`, i, 1+i%12, i, text))
		if _, err = insert.Exec(source, i, string(raw)); err != nil {
			b.Fatal(err)
		}
		if err = writer.write(ctx, source, int64(i), "fixture", s.catalogProjection(raw, int64(i), source)); err != nil {
			b.Fatal(err)
		}
	}
	writer.close()
	insert.Close()
	if err = tx.Commit(); err != nil {
		b.Fatal(err)
	}
	q := defaultBookQuery()
	first, err := s.Books(ctx, q)
	if err != nil {
		b.Fatal(err)
	}
	cases := map[string]BookQuery{"name": q, "date": {Official: "all", Sort: "date-desc", SearchIn: "name"}, "edition_official": {Edition: "dnd-35", Official: "official", Sort: "date-asc", SearchIn: "name"}, "fts_selective": {Official: "all", Sort: "name-asc", SearchIn: "all", Search: "sapphire"}, "fts_broad": {Official: "all", Sort: "date-desc", SearchIn: "all", Search: "dragon"}, "next_page": {Official: "all", Sort: "name-asc", SearchIn: "name", After: first.Next}}
	cases["storefront_date"] = BookQuery{Source: storefront.DTRPG, Official: "all", Sort: "date-desc", SearchIn: "name"}
	cases["storefront_official"] = BookQuery{Source: storefront.DMSGuild, Edition: "dnd-35", Official: "official", Sort: "name-asc", SearchIn: "name"}
	for name, query := range cases {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				page, err := s.Books(ctx, query)
				if err != nil || len(page.Books) != 50 {
					b.Fatal(page, err)
				}
			}
		})
	}
}
