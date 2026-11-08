package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/Rainbows4Blood/Nekonomicon/internal/catalog"
	"github.com/Rainbows4Blood/Nekonomicon/internal/enrichdata"
	"github.com/Rainbows4Blood/Nekonomicon/internal/groups"
	"github.com/Rainbows4Blood/Nekonomicon/internal/metadata"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

type BookQuery struct {
	Source   storefront.Source `json:"source"`
	Edition  string            `json:"edition"`
	Official string            `json:"official"`
	Sort     string            `json:"sort"`
	Search   string            `json:"search"`
	SearchIn string            `json:"searchIn"`
	After    string            `json:"after"`
}
type Book struct {
	Source      storefront.Source `json:"source"`
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Publisher   string            `json:"publisher"`
	ReleaseDate string            `json:"releaseDate"`
	Editions    []string          `json:"editions"`
	Official    *bool             `json:"official"`
	HasCover    bool              `json:"hasCover"`
}
type BookPage struct {
	Books      []Book `json:"books"`
	Next       string `json:"next"`
	CatalogKey string `json:"catalogKey"`
}
type bookCursor struct {
	Key    string
	Source storefront.Source
	ID     int64
	Filter string
}

func searchExpression(q string, scope string) (string, error) {
	if len(q) > 512 {
		return "", errors.New("Search is limited to 512 characters")
	}
	words := strings.FieldsFunc(q, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	if len(words) > 16 {
		return "", errors.New("Use at most 16 search words")
	}
	if len(words) == 0 {
		return "", errors.New("Enter at least one search word")
	}
	for i, w := range words {
		words[i] = `"` + w + `"`
	}
	expression := strings.Join(words, " AND ")
	if scope == "name" {
		expression = "name : (" + expression + ")"
	}
	return expression, nil
}
func bookSelect(q BookQuery) (string, []any, error) {
	if q.Source != "" && !q.Source.Valid() {
		return "", nil, errors.New("Unknown storefront filter")
	}
	valid := q.Edition == ""
	for _, g := range groups.Definitions {
		valid = valid || g.ID == q.Edition
	}
	if !valid {
		return "", nil, errors.New("Unknown D&D edition")
	}
	if q.Official != "all" && q.Official != "official" && q.Official != "unofficial" {
		return "", nil, errors.New("Unknown official-status filter")
	}
	if q.SearchIn != "name" && q.SearchIn != "all" {
		return "", nil, errors.New("Choose name or name and description search")
	}
	date := strings.HasPrefix(q.Sort, "date-")
	desc := strings.HasSuffix(q.Sort, "desc")
	if q.Sort != "name-asc" && q.Sort != "name-desc" && q.Sort != "date-asc" && q.Sort != "date-desc" {
		return "", nil, errors.New("Unknown book sort")
	}
	col, index := "name_key", "dnd_name"
	if date {
		col = "date_asc_key"
		index = "dnd_date_asc"
		if desc {
			index = "dnd_date_desc"
			col = "date_desc_key"
		}
	}
	if q.Official != "all" {
		index = strings.Replace(index, "dnd_", "dnd_official_", 1)
	}
	if q.Source != "" {
		index = strings.Replace(index, "dnd_", "dnd_source_", 1)
	}
	access := ""
	if strings.TrimSpace(q.Search) == "" {
		access = " INDEXED BY " + index
	}
	from := "dnd_listing l" + access + " JOIN dnd_books b ON b.source=l.source AND b.product_id=l.product_id"
	if strings.TrimSpace(q.Search) != "" {
		// Drive selective searches from the FTS matches, then seek each edition entry.
		from = "dnd_search CROSS JOIN dnd_books b ON b.rowid=dnd_search.rowid CROSS JOIN dnd_listing l ON l.source=b.source AND l.product_id=b.product_id"
	}
	query := `SELECT l.source,l.product_id,b.name,b.publisher,b.release_date,b.editions_json,b.official,EXISTS(SELECT 1 FROM covers c WHERE c.source=l.source AND c.product_id=l.product_id),l.` + col + ` FROM ` + from + ` JOIN products p ON p.source=l.source AND p.product_id=l.product_id JOIN catalog_state s ON s.source=l.source AND s.product_id=l.product_id WHERE l.edition=? AND s.version=? AND s.response_fetched_at=p.fetched_at AND s.status IN ('complete','warnings')`
	args := []any{q.Edition, catalog.Version}
	if q.Source != "" {
		query += " AND l.source=?"
		args = append(args, q.Source)
	}
	if q.Official != "all" {
		flag := 0
		if q.Official == "official" {
			flag = 1
		}
		query += " AND l.official=?"
		args = append(args, flag)
	}
	if strings.TrimSpace(q.Search) != "" {
		expression, err := searchExpression(q.Search, q.SearchIn)
		if err != nil {
			return "", nil, err
		}
		query += ` AND dnd_search MATCH ?`
		args = append(args, expression)
	}
	fingerprint := q
	fingerprint.After = ""
	filter, _ := json.Marshal(fingerprint)
	op, dir := ">", " ASC"
	if desc {
		op, dir = "<", " DESC"
	}
	if q.After != "" {
		if len(q.After) > 32768 {
			return "", nil, errors.New("Invalid page cursor")
		}
		raw, err := base64.RawURLEncoding.DecodeString(q.After)
		var cursor bookCursor
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Filter != string(filter) || !cursor.Source.Valid() || cursor.ID <= 0 {
			return "", nil, errors.New("The page changed; refresh the book list")
		}
		seek := `(l.` + col + `,l.source,l.product_id)` + op + `(?,?,?)`
		query += " AND " + seek
		args = append(args, cursor.Key, cursor.Source, cursor.ID)
	}
	query += " ORDER BY "
	query += "l." + col + dir + ",l.source" + dir + ",l.product_id" + dir + " LIMIT 51"
	return query, args, nil
}
func (s *Store) Books(ctx context.Context, q BookQuery) (BookPage, error) {
	result := BookPage{Books: []Book{}}
	if s.Version() != SchemaVersion {
		return result, errors.New("Apply the database update in Data maintenance first")
	}
	query, args, err := bookSelect(q)
	if err != nil {
		return result, err
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	var last bookCursor
	fingerprint := q
	fingerprint.After = ""
	filter, _ := json.Marshal(fingerprint)
	for rows.Next() {
		if len(result.Books) == 50 {
			raw, _ := json.Marshal(last)
			result.Next = base64.RawURLEncoding.EncodeToString(raw)
			break
		}
		var b Book
		var editions string
		var official int
		if err = rows.Scan(&b.Source, &b.ID, &b.Name, &b.Publisher, &b.ReleaseDate, &editions, &official, &b.HasCover, &last.Key); err != nil {
			return result, err
		}
		if err = json.Unmarshal([]byte(editions), &b.Editions); err != nil {
			return result, err
		}
		if official >= 0 {
			v := official == 1
			b.Official = &v
		}
		last.Source, last.ID, last.Filter = b.Source, b.ID, string(filter)
		result.Books = append(result.Books, b)
	}
	return result, rows.Err()
}

type BookFact struct {
	Label string `json:"label"`
	Value string `json:"value"`
}
type BookDetail struct {
	Enrichment        enrichdata.Result `json:"enrichment"`
	PlayersEnriched   bool              `json:"playersEnriched"`
	LevelsEnriched    bool              `json:"levelsEnriched"`
	SettingsEnriched  bool              `json:"settingsEnriched"`
	LocationsEnriched bool              `json:"locationsEnriched"`
	DatesEnriched     bool              `json:"datesEnriched"`
	Book
	Description string     `json:"description"`
	Facts       []BookFact `json:"facts"`
	Creators    []BookFact `json:"creators"`
	Formats     []BookFact `json:"formats"`
	Files       []BookFact `json:"files"`
	Warnings    []string   `json:"warnings"`
	Fetched     string     `json:"fetched"`
	URL         string     `json:"url"`
	CatalogKey  string     `json:"catalogKey"`
}

func (s *Store) BookDetails(ctx context.Context, source storefront.Source, id int64) (BookDetail, error) {
	var d BookDetail
	if !source.Valid() || id <= 0 || s.Version() != SchemaVersion {
		return d, errors.New("Book is not available in this catalog")
	}
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT p.response_json,p.fetched_at,EXISTS(SELECT 1 FROM covers c WHERE c.source=p.source AND c.product_id=p.product_id) FROM products p JOIN dnd_books b USING(source,product_id) JOIN catalog_state s USING(source,product_id) WHERE p.source=? AND p.product_id=? AND s.version=? AND s.response_fetched_at=p.fetched_at AND s.status IN ('complete','warnings')`, source, id, catalog.Version).Scan(&raw, &d.Fetched, &d.HasCover)
	if err != nil {
		return d, err
	}
	p := metadata.Extract(raw, id)
	doc := catalog.Derive(source, id, p)
	d.Source, d.ID, d.Name, d.Publisher, d.ReleaseDate, d.Editions, d.Official = source, id, doc.Name, doc.Publisher, doc.ReleaseDate, doc.Editions, doc.Official
	enrichment, err := s.bookEnrichment(ctx, source, id)
	if err != nil {
		return d, err
	}
	d.Enrichment, d.PlayersEnriched, d.LevelsEnriched = enrichment.Existing, enrichment.PlayersDone, enrichment.LevelsDone
	d.SettingsEnriched, d.LocationsEnriched, d.DatesEnriched = enrichment.SettingsDone, enrichment.LocationsDone, enrichment.DatesDone
	d.Description, d.Warnings, d.URL = doc.Description, doc.Warnings, source.ProductURL(id)
	for _, field := range []struct{ col, label string }{{"page_count_text", "Pages"}, {"price", "Price (€)"}, {"special_price", "Sale price (€)"}, {"lowest_digital_price", "Digital from (€)"}, {"lowest_print_price", "Print from (€)"}, {"isbn", "ISBN"}, {"sku", "SKU"}, {"edition", "Product revision"}, {"rating_average", "Average rating"}, {"review_count", "Reviews"}} {
		if v := catalog.Value(p, field.col); v != nil && fmt.Sprint(v) != "" {
			d.Facts = append(d.Facts, BookFact{field.label, catalog.Limit(fmt.Sprint(v), 4096)})
		}
	}
	for _, spec := range []struct {
		table        metadata.Table
		label, value string
		dest         *[]BookFact
	}{{metadata.Creators, "role", "name", &d.Creators}, {metadata.Options, "option_name", "option_value_name", &d.Formats}, {metadata.Files, "filename", "size_mb_text", &d.Files}} {
		for i, row := range p.Rows[spec.table.Name] {
			if i >= 100 {
				d.Warnings = append(d.Warnings, "Additional entries are retained in the original response")
				break
			}
			fact := BookFact{}
			for n, f := range spec.table.Fields {
				if n < len(row) && row[n] != nil {
					if f.Column == spec.label {
						fact.Label = catalog.Limit(fmt.Sprint(row[n]), 4096)
					}
					if f.Column == spec.value {
						fact.Value = catalog.Limit(fmt.Sprint(row[n]), 4096)
					}
				}
			}
			*spec.dest = append(*spec.dest, fact)
		}
	}
	return d, nil
}

func (s *Store) CoverPath(ctx context.Context, source storefront.Source, id int64) (string, error) {
	var path string
	err := s.db.QueryRowContext(ctx, `SELECT relative_path FROM covers WHERE source=? AND product_id=?`, source, id).Scan(&path)
	return path, err
}
