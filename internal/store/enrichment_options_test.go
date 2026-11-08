package store

import (
	"context"
	"strings"
	"testing"
)

func TestEnrichmentMultiselectMatchesExactValuesWithOrWithinAndAcrossLists(t *testing.T) {
	s := openTest(t)
	seedEnrichedBooks(t, s, 4)
	ctx := context.Background()
	for _, tc := range []struct {
		f    EnrichmentFilter
		want int
	}{
		{EnrichmentFilter{Setting: []string{"ÉBERRON", "Forgotten Realms"}}, 4},
		{EnrichmentFilter{Setting: []string{"eberron", "eberron", "agnostic"}}, 4},
		{EnrichmentFilter{Location: []string{"Site 001", "Site 004"}}, 4},
		{EnrichmentFilter{Location: []string{"Site 001", "Site 004"}, WorldDate: []string{"1404 DR"}, Calendar: []string{"Dalereckoning"}}, 2},
		{EnrichmentFilter{Setting: []string{"forgotten"}}, 0},
		{EnrichmentFilter{Setting: []string{"%' OR 1=1--"}}, 0},
		{EnrichmentFilter{Setting: []string{}}, 8},
	} {
		q := defaultBookQuery()
		q.AdventuresOnly = true
		q.Enrichment = tc.f
		p, err := s.Books(ctx, q)
		if err != nil || len(p.Books) != tc.want {
			t.Fatal(tc, len(p.Books), err)
		}
	}
	for _, values := range [][]string{{""}, {strings.Repeat("x", 4097)}, make([]string, 65)} {
		q := defaultBookQuery()
		q.Enrichment.Setting = values
		if _, err := s.Books(ctx, q); err == nil {
			t.Fatal("invalid selections accepted")
		}
	}
}

func TestEnrichmentOptionsAreCurrentDistinctAndPreserveLabels(t *testing.T) {
	s := openTest(t)
	seedEnrichedBooks(t, s, 4)
	ctx := context.Background()
	for _, kind := range []string{"setting", "location", "world-date", "calendar"} {
		p, err := s.EnrichmentOptions(ctx, kind, "")
		if err != nil || len(p.Options) == 0 {
			t.Fatal(kind, p, err)
		}
		for i, o := range p.Options {
			if o.Label == "" || i > 0 && p.Options[i-1].Value >= o.Value {
				t.Fatal("invalid option order or duplicate", p)
			}
		}
	}
	p, err := s.EnrichmentOptions(ctx, "setting", "")
	if err != nil || len(p.Options) != 3 {
		t.Fatal(p, err)
	}
	labels := map[string]string{}
	for _, o := range p.Options {
		labels[o.Value] = o.Label
	}
	if labels["eberron"] != "Éberron" || labels["forgotten realms"] != "Forgotten Realms" || labels["agnostic"] != "Agnostic (any campaign setting)" {
		t.Fatal(labels)
	}
	// Stale results, removed adventure eligibility and stale catalog projections
	// must not offer choices, even while their old index terms remain on disk.
	if _, err = s.db.Exec(`UPDATE enrichment_results SET response_fetched_at='stale' WHERE source='dtrpg';UPDATE dnd_books SET is_adventure=0 WHERE source='dmsguild' AND product_id=1;UPDATE catalog_state SET version=0 WHERE source='dmsguild' AND product_id=4`); err != nil {
		t.Fatal(err)
	}
	p, err = s.EnrichmentOptions(ctx, "setting", "")
	if err != nil || len(p.Options) != 0 {
		t.Fatal("stale options leaked", p, err)
	}
	if _, err = s.EnrichmentOptions(ctx, "invalid", ""); err == nil {
		t.Fatal("unknown facet accepted")
	}
}

func TestEnrichmentOptionPagesAndLargeSelectionCursors(t *testing.T) {
	s := openTest(t)
	seedEnrichedBooks(t, s, 400)
	ctx := context.Background()
	var after, last string
	seen := map[string]bool{}
	for {
		p, err := s.EnrichmentOptions(ctx, "world-date", after)
		if err != nil || len(p.Options) > 100 {
			t.Fatal(p, err)
		}
		for _, o := range p.Options {
			if seen[o.Value] || o.Value <= last {
				t.Fatal("duplicate or unsorted page", o)
			}
			seen[o.Value] = true
			last = o.Value
		}
		if p.Next == "" {
			break
		}
		after = p.Next
	}
	if len(seen) != 134 {
		t.Fatal(len(seen))
	}
	// Large multi-selections should not produce a cursor bigger than the reader's
	// accepted size. Fingerprinting hashes the complete query, including scope.
	q := defaultBookQuery()
	q.Enrichment.Setting = []string{"eberron"}
	for i := 0; i < 63; i++ {
		q.Enrichment.Setting = append(q.Enrichment.Setting, strings.Repeat("x", 600)+string(rune('A'+i)))
	}
	p, err := s.Books(ctx, q)
	if err != nil || p.Next == "" {
		t.Fatal(p, err)
	}
	if len(p.Next) > 32768 {
		t.Fatal("oversized cursor")
	}
	q.After = p.Next
	if _, err = s.Books(ctx, q); err != nil {
		t.Fatal(err)
	}
	q.Enrichment.Setting = []string{"Forgotten Realms"}
	if _, err = s.Books(ctx, q); err == nil {
		t.Fatal("cursor ignored changed selection")
	}
}
