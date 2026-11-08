package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Rainbows4Blood/Nekonomicon/internal/enrichdata"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

func number(n int) *int { return &n }
func enrichmentFixture(n int) enrichdata.Result {
	setting, calendar := "Forgotten Realms", "Dalereckoning"
	return enrichdata.Result{
		Players:   enrichdata.Range{Min: number(n), Max: number(n + 1), Evidence: "Fixture players"},
		Levels:    enrichdata.Range{Min: number(n - 1), Max: number(n), Evidence: "Fixture levels"},
		Settings:  enrichdata.Settings{Names: []string{setting, "Éberron"}, Agnostic: n%2 == 0, Evidence: "Fixture worlds"},
		Locations: enrichdata.Locations{Items: []enrichdata.Location{{Name: fmt.Sprintf("Site %03d", n), Setting: &setting}, {Name: "Waterdeep", Setting: &setting}}, Evidence: "Fixture sites"},
		Dates:     enrichdata.Dates{Items: []enrichdata.WorldDate{{Date: fmt.Sprintf("%04d DR", 1400+n), Calendar: &calendar, Setting: &setting}}, Evidence: "Fixture dates"},
	}
}

// Storage-only workload: 30k synthetic ~2 KiB responses, both storefronts,
// current enrichment on 80%, WAL/NORMAL and the production indexes. No HTTP or covers.
func BenchmarkEnrichedBookQueries(b *testing.B) {
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
	defer tx.Rollback()
	insert, err := tx.Prepare(`INSERT INTO products VALUES(?,?,'fixture','bench',?)`)
	if err != nil {
		b.Fatal(err)
	}
	writer, err := prepareCatalog(ctx, tx)
	if err != nil {
		b.Fatal(err)
	}
	for i := 1; i <= 30000; i++ {
		source := storefront.DTRPG
		if i%2 == 0 {
			source = storefront.DMSGuild
		}
		raw := []byte(fmt.Sprintf(`{"productId":%d,"publisherId":44,"description":{"name":"Tome %05d","description":%q},"filters":[{"filterId":44705},{"filterId":2110}]}`, i, i, strings.Repeat("A dragon guards the ancient archive. ", 50)))
		if _, err = insert.Exec(source, i, string(raw)); err != nil {
			b.Fatal(err)
		}
		doc := s.catalogProjection(raw, int64(i), source)
		doc.Adventure = true
		if err = writer.write(ctx, source, int64(i), "fixture", doc); err != nil {
			b.Fatal(err)
		}
	}
	writer.close()
	insert.Close()
	r := enrichmentFixture(4)
	players, _ := json.Marshal(r.Players)
	levels, _ := json.Marshal(r.Levels)
	settings, _ := json.Marshal(r.Settings)
	locations, _ := json.Marshal(r.Locations)
	dates, _ := json.Marshal(r.Dates)
	_, err = tx.Exec(`INSERT INTO enrichment_results(source,product_id,version,response_fetched_at,players_json,levels_json,min_player,max_player,min_level,max_level,players_complete,levels_complete,status,message,model,cover_hash,updated_at,players_done,levels_done,settings_json,locations_json,dates_json,settings_done,locations_done,dates_done,settings_complete,locations_complete,dates_complete) SELECT source,product_id,1,fetched_at,?,?,4,5,3,4,1,1,'complete','','fixture','','fixture',1,1,?,?,?,1,1,1,1,1,1 FROM products WHERE product_id%5<>0`, string(players), string(levels), string(settings), string(locations), string(dates))
	if err != nil {
		b.Fatal(err)
	}
	if err = migrateEnrichmentBrowse(ctx, tx); err != nil {
		b.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		b.Fatal(err)
	}
	for _, kind := range []string{"name-asc", "min-player-desc", "setting-asc", "world-date-desc", "combined"} {
		q := defaultBookQuery()
		q.Sort = kind
		if kind == "combined" {
			q.Sort = "min-player-asc"
			q.Source = storefront.DTRPG
			q.Enrichment = EnrichmentFilter{Setting: []string{"eberron"}, Location: []string{"waterdeep"}, MinPlayer: NumberFilter{From: number(3)}}
		}
		b.Run(kind, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				p, err := s.Books(ctx, q)
				if err != nil || len(p.Books) != 50 {
					b.Fatal(len(p.Books), err)
				}
			}
		})
	}
}
func seedEnrichedBooks(t *testing.T, s *Store, n int) {
	t.Helper()
	seedBooks(t, s, n)
	if _, err := s.db.Exec(`UPDATE dnd_books SET is_adventure=1`); err != nil {
		t.Fatal(err)
	}
	for _, source := range []storefront.Source{storefront.DTRPG, storefront.DMSGuild} {
		for id := 1; id <= n; id++ {
			if id%3 == 0 {
				continue
			}
			b := EnrichmentBook{Source: source, ID: int64(id), PlayersDone: true, LevelsDone: true, SettingsDone: true, LocationsDone: true, DatesDone: true}
			if err := s.db.QueryRow(`SELECT fetched_at FROM products WHERE source=? AND product_id=?`, source, id).Scan(&b.Fetched); err != nil {
				t.Fatal(err)
			}
			r := enrichmentFixture(id)
			if id%3 == 2 {
				r = enrichdata.Result{}
			}
			if err := s.SaveEnrichment(context.Background(), b, r, "fixture", ""); err != nil {
				t.Fatal(err)
			}
		}
	}
}
func TestEnrichedBooksFiltersAndFreshness(t *testing.T) {
	s := openTest(t)
	seedEnrichedBooks(t, s, 4)
	ctx := context.Background()
	cases := []struct {
		f    EnrichmentFilter
		want int
	}{
		{EnrichmentFilter{Setting: []string{"EBERRON"}}, 4}, {EnrichmentFilter{Setting: []string{"Forgotten Realms"}, Locations: "known", Location: []string{"waterDEEP"}, WorldDate: []string{"1404 DR"}, Calendar: []string{"Dalereckoning"}}, 2},
		{EnrichmentFilter{Settings: "agnostic"}, 2}, {EnrichmentFilter{Settings: "unknown"}, 2}, {EnrichmentFilter{Settings: "pending"}, 2},
		{EnrichmentFilter{Players: "unknown", Levels: "unknown", Locations: "unknown", Dates: "unknown"}, 2},
		{EnrichmentFilter{MinPlayer: NumberFilter{From: number(2)}, MaxPlayer: NumberFilter{To: number(5)}, MinLevel: NumberFilter{From: number(3)}, MaxLevel: NumberFilter{To: number(4)}}, 2},
		{EnrichmentFilter{MinLevel: NumberFilter{From: number(0), To: number(0)}}, 2},
		{EnrichmentFilter{Setting: []string{"%' OR 1=1--"}}, 0},
	}
	for _, tc := range cases {
		q := defaultBookQuery()
		q.Enrichment = tc.f
		p, err := s.Books(ctx, q)
		if err != nil || len(p.Books) != tc.want {
			t.Fatalf("%+v: %d want %d: %v", tc.f, len(p.Books), tc.want, err)
		}
	}
	q := defaultBookQuery()
	q.Enrichment.Setting = []string{"eberron"}
	q.Source = storefront.DTRPG
	q.Edition = "dnd-35"
	q.Official = "all"
	q.Search = "sapphire"
	q.SearchIn = "all"
	p, err := s.Books(ctx, q)
	if err != nil || len(p.Books) != 2 {
		t.Fatal(p, err)
	}
	// A new source revision hides all old enrichment, even while terms remain.
	if _, err = s.db.Exec(`UPDATE products SET fetched_at='new';UPDATE catalog_state SET response_fetched_at='new'`); err != nil {
		t.Fatal(err)
	}
	q = defaultBookQuery()
	q.Enrichment.Setting = []string{"eberron"}
	p, err = s.Books(ctx, q)
	if err != nil || len(p.Books) != 0 {
		t.Fatal("stale terms matched", p, err)
	}
	q.Enrichment = EnrichmentFilter{Settings: "pending"}
	p, err = s.Books(ctx, q)
	if err != nil || len(p.Books) != 8 {
		t.Fatal(p, err)
	}
}
func TestEnrichedSortsPageWithoutDuplicatesAndMissingLast(t *testing.T) {
	s := openTest(t)
	seedEnrichedBooks(t, s, 80)
	ctx := context.Background()
	for _, kind := range []string{"min-player", "max-player", "min-level", "max-level", "setting", "location", "world-date", "calendar"} {
		for _, dir := range []string{"asc", "desc"} {
			q := defaultBookQuery()
			q.Sort = kind + "-" + dir
			seen := map[string]bool{}
			last := ""
			missing := false
			for {
				p, err := s.Books(ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				for _, v := range p.Books {
					id := fmt.Sprint(v.Source, v.ID)
					if seen[id] {
						t.Fatal("duplicate", q.Sort, id)
					}
					seen[id] = true
					var key string
					s.db.QueryRow(`SELECT coalesce(min(value),'') FROM enrichment_terms WHERE source=? AND product_id=? AND kind=?`, v.Source, v.ID, kind).Scan(&key)
					if key == "" {
						missing = true
					} else {
						if missing {
							t.Fatal("missing value sorted first", q.Sort)
						}
						if last != "" && (dir == "asc" && last > key || dir == "desc" && last < key) {
							t.Fatal("incorrect order", q.Sort, last, key)
						}
						last = key
					}
				}
				if p.Next == "" {
					break
				}
				q.After = p.Next
			}
			if len(seen) != 160 {
				t.Fatal(q.Sort, len(seen))
			}
		}
	}
	q := defaultBookQuery()
	q.Sort = "setting-asc"
	p, _ := s.Books(ctx, q)
	q.After = p.Next
	q.Enrichment.Setting = []string{"eberron"}
	if _, err := s.Books(ctx, q); err == nil {
		t.Fatal("cursor accepted after filter changed")
	}
	for _, q := range []BookQuery{{Sort: "min-player-asc-junk"}, {Sort: "min-player-asc", Enrichment: EnrichmentFilter{MinPlayer: NumberFilter{From: number(9), To: number(1)}}}} {
		q.SearchIn = "name"
		q.Official = "all"
		if _, err := s.Books(ctx, q); err == nil {
			t.Fatal("invalid query accepted")
		}
	}
}
func TestEnrichmentBrowseMigrationAndAtomicSaves(t *testing.T) {
	s := openTest(t)
	seedEnrichedBooks(t, s, 4)
	ctx := context.Background()
	if _, err := s.db.Exec(`DROP TABLE enrichment_terms;PRAGMA user_version=9`); err != nil {
		t.Fatal(err)
	}
	dir := s.Dir
	s.Close()
	var err error
	s, err = InspectContext(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Version() != 9 {
		t.Fatal("inspection migrated")
	}
	cancelled, cancel := context.WithCancel(ctx)
	if err = s.Upgrade(cancelled, func(msg string) {
		if msg == "Indexing adventure enrichment filters…" {
			cancel()
		}
	}); err == nil {
		t.Fatal("cancel succeeded")
	}
	var count int
	s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='enrichment_terms'`).Scan(&count)
	if count != 0 {
		t.Fatal("cancelled migration retained table")
	}
	if err = s.Upgrade(ctx, nil); err != nil {
		t.Fatal(err)
	}
	q := defaultBookQuery()
	q.Enrichment.Setting = []string{"eberron"}
	p, err := s.Books(ctx, q)
	if err != nil || len(p.Books) != 4 {
		t.Fatal("backfill lost saved data", p, err)
	}
	b := EnrichmentBook{Source: storefront.DTRPG, ID: 1}
	s.db.QueryRow(`SELECT fetched_at FROM products WHERE source='dtrpg' AND product_id=1`).Scan(&b.Fetched)
	s.db.Exec(`CREATE TRIGGER fail_terms BEFORE INSERT ON enrichment_terms BEGIN SELECT RAISE(ABORT,'synthetic');END`)
	if err = s.SaveEnrichment(ctx, b, enrichmentFixture(8), "", ""); err == nil {
		t.Fatal("index failure accepted")
	}
	r, err := s.BookEnrichment(ctx, b.Source, b.ID)
	if err != nil || r.Players.Min == nil || *r.Players.Min != 1 {
		t.Fatal("result escaped rollback", r, err)
	}
	s.db.Exec(`DROP TRIGGER fail_terms`)
	if err = s.SaveEnrichment(ctx, b, enrichmentFixture(8), "", ""); err != nil {
		t.Fatal(err)
	}
	q.Enrichment = EnrichmentFilter{MinPlayer: NumberFilter{From: number(8)}}
	p, err = s.Books(ctx, q)
	if err != nil || len(p.Books) != 1 {
		t.Fatal("terms not replaced", p, err)
	}
	sql, args, _ := bookSelect(q)
	rows, err := s.db.Query("EXPLAIN QUERY PLAN "+sql, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var a, b, c int
		var d string
		rows.Scan(&a, &b, &c, &d)
		plan.WriteString(d)
	}
	if !strings.Contains(plan.String(), "enrichment_terms_value") {
		t.Fatal("filter did not use value index", plan.String())
	}
}
