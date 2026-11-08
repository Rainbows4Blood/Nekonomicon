package store

import (
	"context"
	"fmt"
	"testing"
)

func TestAdventureScopeFiltersBeforePagination(t *testing.T) {
	s := openTest(t)
	seedBooks(t, s, 73)
	ctx := context.Background()
	if _, err := s.db.Exec(`UPDATE dnd_books SET is_adventure=1 WHERE product_id%2=0`); err != nil {
		t.Fatal(err)
	}
	q := defaultBookQuery()
	library, err := s.Books(ctx, q)
	if err != nil || len(library.Books) != 50 {
		t.Fatal(library, err)
	}
	q.AdventuresOnly = true
	q.After = library.Next
	if _, err = s.Books(ctx, q); err == nil {
		t.Fatal("library cursor accepted in adventures")
	}
	q.After = ""
	for _, sort := range []string{"name-asc", "name-desc", "date-asc", "date-desc", "setting-asc"} {
		q.Sort = sort
		q.After = ""
		seen := map[string]bool{}
		for {
			p, err := s.Books(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for _, b := range p.Books {
				key := fmt.Sprint(b.Source, b.ID)
				if b.ID%2 != 0 || seen[key] {
					t.Fatal("not an adventure or duplicate", b)
				}
				seen[key] = true
			}
			if p.Next == "" {
				break
			}
			q.After = p.Next
		}
		if len(seen) != 72 {
			t.Fatal(sort, len(seen))
		}
	}
	q.After = ""
	q.Enrichment.Settings = "pending"
	q.Source = "dtrpg"
	q.Search = "sapphire"
	q.SearchIn = "all"
	q.Edition = "dnd-35"
	p, err := s.Books(ctx, q)
	if err != nil || len(p.Books) != 36 {
		t.Fatal("combined filters/pending included rulebooks", p, err)
	}
	q = defaultBookQuery()
	p, err = s.Books(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	foundNonAdventure := false
	for _, b := range p.Books {
		foundNonAdventure = foundNonAdventure || b.ID%2 != 0
	}
	if !foundNonAdventure {
		t.Fatal("library excluded general books")
	}
}
