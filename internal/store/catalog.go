package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Rainbows4Blood/Nekonomicon/internal/catalog"
	"github.com/Rainbows4Blood/Nekonomicon/internal/metadata"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

func catalogSchema() string {
	return `CREATE TABLE IF NOT EXISTS catalog_state(source TEXT NOT NULL,product_id INTEGER NOT NULL,version INTEGER NOT NULL,response_fetched_at TEXT NOT NULL,status TEXT NOT NULL,message TEXT NOT NULL,warnings_json TEXT NOT NULL,processed_at TEXT NOT NULL,PRIMARY KEY(source,product_id),FOREIGN KEY(source,product_id) REFERENCES products(source,product_id));
CREATE TABLE IF NOT EXISTS catalog_jobs(source TEXT PRIMARY KEY,version INTEGER NOT NULL,state TEXT NOT NULL,total INTEGER NOT NULL,processed INTEGER NOT NULL,failed INTEGER NOT NULL,warned INTEGER NOT NULL,last_id INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS dnd_books(rowid INTEGER PRIMARY KEY,source TEXT NOT NULL,product_id INTEGER NOT NULL,name TEXT NOT NULL,description TEXT NOT NULL,publisher TEXT NOT NULL,release_date TEXT NOT NULL,editions_json TEXT NOT NULL,official INTEGER NOT NULL,official_reason TEXT NOT NULL,UNIQUE(source,product_id),FOREIGN KEY(source,product_id) REFERENCES products(source,product_id));
CREATE VIRTUAL TABLE IF NOT EXISTS dnd_search USING fts5(name,description,content='dnd_books',content_rowid='rowid',tokenize='unicode61 remove_diacritics 2');
CREATE TRIGGER IF NOT EXISTS dnd_search_insert AFTER INSERT ON dnd_books BEGIN INSERT INTO dnd_search(rowid,name,description) VALUES(new.rowid,new.name,new.description); END;
CREATE TRIGGER IF NOT EXISTS dnd_search_delete AFTER DELETE ON dnd_books BEGIN INSERT INTO dnd_search(dnd_search,rowid,name,description) VALUES('delete',old.rowid,old.name,old.description); END;
CREATE TABLE IF NOT EXISTS dnd_listing(edition TEXT NOT NULL,source TEXT NOT NULL,product_id INTEGER NOT NULL,official INTEGER NOT NULL,name_key TEXT NOT NULL,date_asc_key TEXT NOT NULL,date_desc_key TEXT NOT NULL,PRIMARY KEY(edition,source,product_id),FOREIGN KEY(source,product_id) REFERENCES dnd_books(source,product_id));
CREATE INDEX IF NOT EXISTS dnd_listing_product ON dnd_listing(source,product_id);
CREATE INDEX IF NOT EXISTS dnd_name ON dnd_listing(edition,name_key,source,product_id);
CREATE INDEX IF NOT EXISTS dnd_official_name ON dnd_listing(edition,official,name_key,source,product_id);
CREATE INDEX IF NOT EXISTS dnd_date_asc ON dnd_listing(edition,date_asc_key,source,product_id);
CREATE INDEX IF NOT EXISTS dnd_date_desc ON dnd_listing(edition,date_desc_key DESC,source DESC,product_id DESC);
CREATE INDEX IF NOT EXISTS dnd_official_date_asc ON dnd_listing(edition,official,date_asc_key,source,product_id);
CREATE INDEX IF NOT EXISTS dnd_official_date_desc ON dnd_listing(edition,official,date_desc_key DESC,source DESC,product_id DESC);`
}

// Created explicitly by schema maintenance; SQLite also maintains these indexes
// for live crawls and existing listing rows, without re-extracting raw responses.
func storefrontIndexSchema() string {
	return `CREATE INDEX IF NOT EXISTS products_freshness ON products(source,product_id,fetched_at);
CREATE INDEX IF NOT EXISTS dnd_source_name ON dnd_listing(edition,source,name_key,product_id);
CREATE INDEX IF NOT EXISTS dnd_source_official_name ON dnd_listing(edition,source,official,name_key,product_id);
CREATE INDEX IF NOT EXISTS dnd_source_date_asc ON dnd_listing(edition,source,date_asc_key,product_id);
CREATE INDEX IF NOT EXISTS dnd_source_date_desc ON dnd_listing(edition,source,date_desc_key DESC,product_id DESC);
CREATE INDEX IF NOT EXISTS dnd_source_official_date_asc ON dnd_listing(edition,source,official,date_asc_key,product_id);
CREATE INDEX IF NOT EXISTS dnd_source_official_date_desc ON dnd_listing(edition,source,official,date_desc_key DESC,product_id DESC);`
}

type catalogWriter struct{ clearListings, clearBook, book, listing, state *sql.Stmt }

func prepareCatalog(ctx context.Context, tx *sql.Tx) (*catalogWriter, error) {
	w := &catalogWriter{}
	statements := []struct {
		dest **sql.Stmt
		q    string
	}{
		{&w.clearListings, `DELETE FROM dnd_listing WHERE source=? AND product_id=?`},
		{&w.clearBook, `DELETE FROM dnd_books WHERE source=? AND product_id=?`},
		{&w.book, `INSERT INTO dnd_books(source,product_id,name,description,publisher,release_date,editions_json,official,official_reason,is_adventure) VALUES(?,?,?,?,?,?,?,?,?,?)`},
		{&w.listing, `INSERT INTO dnd_listing VALUES(?,?,?,?,?,?,?)`},
		{&w.state, `INSERT INTO catalog_state VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(source,product_id) DO UPDATE SET version=excluded.version,response_fetched_at=excluded.response_fetched_at,status=excluded.status,message=excluded.message,warnings_json=excluded.warnings_json,processed_at=excluded.processed_at`},
	}
	for _, s := range statements {
		var err error
		*s.dest, err = tx.PrepareContext(ctx, s.q)
		if err != nil {
			w.close()
			return nil, err
		}
	}
	return w, nil
}
func (w *catalogWriter) close() {
	for _, s := range []*sql.Stmt{w.clearListings, w.clearBook, w.book, w.listing, w.state} {
		if s != nil {
			s.Close()
		}
	}
}
func (w *catalogWriter) write(ctx context.Context, source storefront.Source, id int64, fetched string, d catalog.Document) error {
	status, msg := "complete", ""
	if d.Failure != "" {
		status, msg = "failed", d.Failure
	} else {
		if len(d.Warnings) > 0 {
			status, msg = "warnings", "Some source fields or grouping evidence need attention"
		}
		if _, err := w.clearListings.ExecContext(ctx, source, id); err != nil {
			return err
		}
		if _, err := w.clearBook.ExecContext(ctx, source, id); err != nil {
			return err
		}
		if len(d.Editions) > 0 {
			official := -1
			if d.Official != nil {
				official = 0
				if *d.Official {
					official = 1
				}
			}
			editions, _ := json.Marshal(d.Editions)
			if _, err := w.book.ExecContext(ctx, source, id, d.Name, d.Description, d.Publisher, d.ReleaseDate, string(editions), official, d.OfficialReason, d.Adventure); err != nil {
				return err
			}
			dateAsc := d.ReleaseDay
			if dateAsc == "" {
				dateAsc = "9999-99-99"
			}
			// The empty edition is a single all-editions entry, avoiding duplicates for
			// books with multiple memberships. Every filter/sort combination can seek.
			for _, edition := range append([]string{""}, d.Editions...) {
				if _, err := w.listing.ExecContext(ctx, edition, source, id, official, d.NameKey, dateAsc, d.ReleaseDay); err != nil {
					return err
				}
			}
		}
	}
	warnings, _ := json.Marshal(d.Warnings)
	_, err := w.state.ExecContext(ctx, source, id, catalog.Version, fetched, status, msg, string(warnings), now())
	return err
}

// Indexing reads original responses through the same mappings used during crawl.
// It is independent of whether older metadata/group maintenance has run yet.
func (s *Store) catalogProjection(raw []byte, id int64, source storefront.Source) catalog.Document {
	return catalog.Derive(source, id, metadata.Extract(raw, id))
}

type CatalogSummary = MetadataSummary
type CatalogJob = MetadataJob

func (s *Store) CatalogSummary(ctx context.Context, source storefront.Source) (CatalogSummary, error) {
	var v CatalogSummary
	err := s.db.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(m.version=? AND m.response_fetched_at=p.fetched_at AND m.status IN ('complete','warnings')),0),coalesce(sum(m.version=? AND m.response_fetched_at=p.fetched_at AND m.status='warnings'),0),coalesce(sum(m.version=? AND m.response_fetched_at=p.fetched_at AND m.status='failed'),0) FROM products p INDEXED BY products_freshness LEFT JOIN catalog_state m USING(source,product_id) WHERE (?='' OR p.source=?)`, catalog.Version, catalog.Version, catalog.Version, source, source).Scan(&v.Total, &v.Current, &v.Warnings, &v.Failed)
	v.Pending = v.Total - v.Current
	return v, err
}
func (s *Store) CatalogJob(ctx context.Context, source storefront.Source) (*CatalogJob, error) {
	j := CatalogJob{Source: source}
	err := s.db.QueryRowContext(ctx, `SELECT version,state,total,processed,failed,warned,last_id FROM catalog_jobs WHERE source=?`, source).Scan(&j.Version, &j.State, &j.Total, &j.Processed, &j.Failed, &j.Warned, &j.LastID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &j, err
}

func (s *Store) CatalogPage(ctx context.Context, source storefront.Source, after int64) ([]MetadataRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.product_id,p.fetched_at,p.response_json FROM products p INDEXED BY products_freshness LEFT JOIN catalog_state m USING(source,product_id) WHERE p.source=? AND p.product_id>? AND (m.version IS NULL OR m.version<>? OR m.response_fetched_at<>p.fetched_at OR m.status='failed') ORDER BY p.product_id LIMIT 64`, source, after, catalog.Version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MetadataRow
	size := 0
	for rows.Next() {
		var r MetadataRow
		if err = rows.Scan(&r.ID, &r.Fetched, &r.JSON); err != nil {
			return nil, err
		}
		out = append(out, r)
		size += len(r.JSON)
		if size >= 4<<20 {
			break
		}
	}
	return out, rows.Err()
}

// The sequential cursor advances in the same transaction as every projection
// and outcome. Only this maintenance pass uses a cursor; crawls remain unordered.
func (s *Store) CommitCatalog(ctx context.Context, j CatalogJob, rows []MetadataRow) (CatalogJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	original := j
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return original, err
	}
	defer tx.Rollback()
	w, err := prepareCatalog(ctx, tx)
	if err != nil {
		return original, err
	}
	defer w.close()
	check, err := tx.PrepareContext(ctx, `SELECT fetched_at FROM products WHERE source=? AND product_id=?`)
	if err != nil {
		return original, err
	}
	defer check.Close()
	for _, r := range rows {
		if r.ID <= j.LastID {
			return original, errors.New("Index batch is not in product order")
		}
		var current string
		if err = check.QueryRowContext(ctx, j.Source, r.ID).Scan(&current); err != nil {
			return original, err
		}
		if current != r.Fetched {
			return original, errors.New("Product changed during indexing")
		}
		projection := s.catalogProjection(r.JSON, r.ID, j.Source)
		if err = w.write(ctx, j.Source, r.ID, r.Fetched, projection); err != nil {
			return original, err
		}
		j.Processed++
		j.LastID = r.ID
		if projection.Failure != "" {
			j.Failed++
		} else if len(projection.Warnings) > 0 {
			j.Warned++
		}
	}
	updated, err := tx.ExecContext(ctx, `UPDATE catalog_jobs SET processed=?,failed=?,warned=?,last_id=? WHERE source=? AND state='running' AND version=? AND last_id=?`, j.Processed, j.Failed, j.Warned, j.LastID, j.Source, j.Version, original.LastID)
	if err != nil {
		return original, err
	}
	if n, err := updated.RowsAffected(); err != nil || n != 1 {
		return original, errors.New("Maintenance progress changed before commit")
	}
	if err = tx.Commit(); err != nil {
		return original, err
	}
	return j, nil
}

func (s *Store) CatalogIssues(ctx context.Context, source storefront.Source, offset int) ([]MetadataIssue, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.source,m.product_id,m.status,m.message,m.warnings_json FROM catalog_state m JOIN products p USING(source,product_id) WHERE (?='' OR m.source=?) AND m.version=? AND m.response_fetched_at=p.fetched_at AND m.status IN ('warnings','failed') ORDER BY m.source,m.product_id LIMIT 50 OFFSET ?`, source, source, catalog.Version, max(0, offset))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MetadataIssue{}
	for rows.Next() {
		var r MetadataIssue
		var raw string
		if err = rows.Scan(&r.Source, &r.ID, &r.Status, &r.Message, &raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &r.Warnings); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
