package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Rainbows4Blood/Nekonomicon/internal/catalog"
	"github.com/Rainbows4Blood/Nekonomicon/internal/enrichdata"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

// Small, normalized search terms avoid parsing enrichment JSON for every book
// during browsing. The original result and evidence remain authoritative.
const enrichmentBrowseSchema = `CREATE TABLE IF NOT EXISTS enrichment_terms(
 source TEXT NOT NULL,product_id INTEGER NOT NULL,kind TEXT NOT NULL,value TEXT NOT NULL,
 PRIMARY KEY(source,product_id,kind,value),
 FOREIGN KEY(source,product_id) REFERENCES products(source,product_id));
 CREATE INDEX IF NOT EXISTS enrichment_terms_value ON enrichment_terms(kind,value,source,product_id);`

func writeEnrichmentTerms(ctx context.Context, tx *sql.Tx, source storefront.Source, id int64, r enrichdata.Result) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM enrichment_terms WHERE source=? AND product_id=?`, source, id); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO enrichment_terms VALUES(?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	values := map[string][]string{}
	for kind, n := range map[string]*int{"min-player": r.Players.Min, "max-player": r.Players.Max, "min-level": r.Levels.Min, "max-level": r.Levels.Max} {
		if n != nil {
			values[kind] = []string{fmt.Sprintf("%05d", *n)}
		}
	}
	values["setting"] = append(values["setting"], r.Settings.Names...)
	if r.Settings.Agnostic {
		values["setting"] = append(values["setting"], "Agnostic")
	}
	for _, v := range r.Locations.Items {
		values["location"] = append(values["location"], v.Name)
	}
	for _, v := range r.Dates.Items {
		values["world-date"] = append(values["world-date"], v.Date)
		if v.Calendar != nil {
			values["calendar"] = append(values["calendar"], *v.Calendar)
		}
	}
	for kind, terms := range values {
		for _, v := range terms {
			if _, err = stmt.ExecContext(ctx, source, id, kind, catalog.NameKey(strings.TrimSpace(v))); err != nil {
				return err
			}
		}
	}
	return nil
}

// Schema maintenance is atomic/cancellable and reads saved enrichments in
// bounded batches. Opening a catalog never invokes this backfill.
func migrateEnrichmentBrowse(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, enrichmentBrowseSchema); err != nil {
		return err
	}
	var source storefront.Source
	var id int64
	for {
		rows, err := tx.QueryContext(ctx, `SELECT source,product_id,players_json,levels_json,settings_json,locations_json,dates_json FROM enrichment_results WHERE (source,product_id)>(?,?) ORDER BY source,product_id LIMIT 64`, source, id)
		if err != nil {
			return err
		}
		var batch []EnrichmentBook
		for rows.Next() {
			var b EnrichmentBook
			var p, l, s, loc, d string
			if err = rows.Scan(&b.Source, &b.ID, &p, &l, &s, &loc, &d); err == nil {
				err = decodeEnrichment(&b.Existing, p, l, s, loc, d)
			}
			if err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, b)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		for _, b := range batch {
			if err = writeEnrichmentTerms(ctx, tx, b.Source, b.ID, b.Existing); err != nil {
				return err
			}
			source, id = b.Source, b.ID
		}
	}
}

type NumberFilter struct {
	From *int `json:"from"`
	To   *int `json:"to"`
}
type EnrichmentFilter struct {
	MinPlayer NumberFilter `json:"minPlayer"`
	MaxPlayer NumberFilter `json:"maxPlayer"`
	MinLevel  NumberFilter `json:"minLevel"`
	MaxLevel  NumberFilter `json:"maxLevel"`
	Players   string       `json:"players"`
	Levels    string       `json:"levels"`
	Settings  string       `json:"settings"`
	Locations string       `json:"locations"`
	Dates     string       `json:"dates"`
	Setting   []string     `json:"setting"`
	Location  []string     `json:"location"`
	WorldDate []string     `json:"worldDate"`
	Calendar  []string     `json:"calendar"`
}

func enrichmentSort(sort string) string {
	kind, _, ok := strings.Cut(sort, "-asc")
	if !ok {
		kind, _, ok = strings.Cut(sort, "-desc")
	}
	if !ok || (sort != kind+"-asc" && sort != kind+"-desc") {
		return ""
	}
	switch kind {
	case "min-player", "max-player", "min-level", "max-level", "setting", "location", "world-date", "calendar":
		return kind
	}
	return ""
}

func enrichmentWhere(f EnrichmentFilter) (string, []any, error) {
	var clauses []string
	var args []any
	for _, v := range []struct {
		kind string
		f    NumberFilter
	}{{"min-player", f.MinPlayer}, {"max-player", f.MaxPlayer}, {"min-level", f.MinLevel}, {"max-level", f.MaxLevel}} {
		if v.f.From == nil && v.f.To == nil {
			continue
		}
		minimum := 0
		if strings.HasSuffix(v.kind, "player") {
			minimum = 1
		}
		for _, n := range []*int{v.f.From, v.f.To} {
			if n != nil && (*n < minimum || *n > 10000) {
				return "", nil, errors.New("Numeric filters are outside the supported range")
			}
		}
		if v.f.From != nil && v.f.To != nil && *v.f.From > *v.f.To {
			return "", nil, errors.New("Filter From must not exceed To")
		}
		sub := `SELECT source,product_id FROM enrichment_terms WHERE kind=?`
		args = append(args, v.kind)
		if v.f.From != nil {
			sub += " AND value>=?"
			args = append(args, fmt.Sprintf("%05d", *v.f.From))
		}
		if v.f.To != nil {
			sub += " AND value<=?"
			args = append(args, fmt.Sprintf("%05d", *v.f.To))
		}
		clauses = append(clauses, `e.source IS NOT NULL AND (l.source,l.product_id) IN (`+sub+`)`)
	}
	for _, v := range []struct {
		kind   string
		values []string
	}{{"setting", f.Setting}, {"location", f.Location}, {"world-date", f.WorldDate}, {"calendar", f.Calendar}} {
		if len(v.values) == 0 {
			continue
		}
		if len(v.values) > 64 {
			return "", nil, errors.New("Select at most 64 values per enrichment filter")
		}
		keys := []string{}
		seen := map[string]bool{}
		for _, value := range v.values {
			if len(value) > 4096 || strings.TrimSpace(value) == "" {
				return "", nil, errors.New("Invalid enrichment filter value")
			}
			key := catalog.NameKey(strings.TrimSpace(value))
			if !seen[key] {
				keys = append(keys, key)
				seen[key] = true
			}
		}
		clauses = append(clauses, "e.source IS NOT NULL AND (l.source,l.product_id) IN (SELECT source,product_id FROM enrichment_terms WHERE kind=? AND value IN ("+strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")+"))")
		args = append(args, v.kind)
		for _, key := range keys {
			args = append(args, key)
		}
	}
	for _, v := range []struct{ field, state string }{{"players", f.Players}, {"levels", f.Levels}, {"settings", f.Settings}, {"locations", f.Locations}, {"dates", f.Dates}} {
		switch v.state {
		case "", "all":
		case "known":
			clauses = append(clauses, "e."+v.field+"_complete=1")
		case "unknown":
			clauses = append(clauses, "e."+v.field+"_done=1 AND e."+v.field+"_complete=0")
		case "pending":
			clauses = append(clauses, "coalesce(e."+v.field+"_done,0)=0")
		case "agnostic":
			if v.field != "settings" {
				return "", nil, errors.New("Invalid enrichment filter")
			}
			clauses = append(clauses, `e.source IS NOT NULL AND json_extract(e.settings_json,'$.agnostic')=1`)
		default:
			return "", nil, errors.New("Invalid enrichment filter")
		}
	}
	if len(clauses) == 0 {
		return "", args, nil
	}
	return " AND (" + strings.Join(clauses, ") AND (") + ")", args, nil
}
