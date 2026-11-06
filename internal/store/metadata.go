package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Rainbows4Blood/Nekonomicon/internal/metadata"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

func metadataSchema() string {
	var q strings.Builder
	for _, t := range append([]metadata.Table{metadata.Product}, metadata.Children...) {
		fmt.Fprintf(&q, "CREATE TABLE IF NOT EXISTS %s(source TEXT NOT NULL,product_id INTEGER NOT NULL", t.Name)
		for _, f := range t.Fields {
			fmt.Fprintf(&q, ",%s %s", f.Column, metadata.SQLType(f))
		}
		if t.Name == metadata.Product.Name {
			q.WriteString(",extraction_version INTEGER NOT NULL,response_fetched_at TEXT NOT NULL,response_sha256 TEXT NOT NULL,PRIMARY KEY(source,product_id)")
		} else {
			q.WriteString(",PRIMARY KEY(source,product_id")
			for _, f := range t.Fields {
				if f.Column == "role" || f.Column == "kind" || f.Column == "taxonomy_ordinal" || f.Column == "ordinal" {
					q.WriteString("," + f.Column)
				}
			}
			q.WriteString(")")
		}
		q.WriteString(",FOREIGN KEY(source,product_id) REFERENCES products(source,product_id));\n")
	}
	q.WriteString(`CREATE TABLE IF NOT EXISTS metadata_state(source TEXT NOT NULL,product_id INTEGER NOT NULL,version INTEGER NOT NULL,response_fetched_at TEXT NOT NULL,status TEXT NOT NULL CHECK(status IN ('complete','warnings','failed')),message TEXT NOT NULL,warnings_json TEXT NOT NULL,processed_at TEXT NOT NULL,PRIMARY KEY(source,product_id),FOREIGN KEY(source,product_id) REFERENCES products(source,product_id));
CREATE TABLE IF NOT EXISTS metadata_jobs(source TEXT PRIMARY KEY,version INTEGER NOT NULL,state TEXT NOT NULL,total INTEGER NOT NULL,processed INTEGER NOT NULL,failed INTEGER NOT NULL,warned INTEGER NOT NULL,last_id INTEGER NOT NULL);
CREATE VIEW IF NOT EXISTS current_product_metadata AS SELECT m.* FROM product_metadata m JOIN products p USING(source,product_id) JOIN metadata_state s USING(source,product_id) WHERE m.response_fetched_at=p.fetched_at AND s.response_fetched_at=p.fetched_at AND s.status IN ('complete','warnings') AND m.extraction_version=`)
	fmt.Fprintf(&q, "%d AND s.version=%d;", metadata.Version, metadata.Version)
	return q.String()
}

type metadataWriter struct {
	insert, clear map[string]*sql.Stmt
	state         *sql.Stmt
}

func prepareMetadata(ctx context.Context, tx *sql.Tx) (*metadataWriter, error) {
	w := &metadataWriter{insert: map[string]*sql.Stmt{}, clear: map[string]*sql.Stmt{}}
	var err error
	for _, t := range append([]metadata.Table{metadata.Product}, metadata.Children...) {
		cols := []string{"source", "product_id"}
		for _, f := range t.Fields {
			cols = append(cols, f.Column)
		}
		if t.Name == metadata.Product.Name {
			cols = append(cols, "extraction_version", "response_fetched_at", "response_sha256")
		}
		q := "INSERT INTO " + t.Name + "(" + strings.Join(cols, ",") + ") VALUES(" + strings.TrimRight(strings.Repeat("?,", len(cols)), ",") + ")"
		w.insert[t.Name], err = tx.PrepareContext(ctx, q)
		if err != nil {
			w.close()
			return nil, err
		}
		w.clear[t.Name], err = tx.PrepareContext(ctx, "DELETE FROM "+t.Name+" WHERE source=? AND product_id=?")
		if err != nil {
			w.close()
			return nil, err
		}
	}
	w.state, err = tx.PrepareContext(ctx, `INSERT INTO metadata_state VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(source,product_id) DO UPDATE SET version=excluded.version,response_fetched_at=excluded.response_fetched_at,status=excluded.status,message=excluded.message,warnings_json=excluded.warnings_json,processed_at=excluded.processed_at`)
	if err != nil {
		w.close()
		return nil, err
	}
	return w, nil
}
func (w *metadataWriter) close() {
	for _, s := range w.insert {
		if s != nil {
			s.Close()
		}
	}
	for _, s := range w.clear {
		if s != nil {
			s.Close()
		}
	}
	if w.state != nil {
		w.state.Close()
	}
}
func (w *metadataWriter) write(ctx context.Context, source storefront.Source, id int64, fetched string, p metadata.Projection) error {
	status := "complete"
	if p.Failure != "" {
		status = "failed"
	} else {
		if len(p.Warnings) > 0 {
			status = "warnings"
		}
		for _, t := range append([]metadata.Table{metadata.Product}, metadata.Children...) {
			if _, err := w.clear[t.Name].ExecContext(ctx, source, id); err != nil {
				return err
			}
			rows := p.Rows[t.Name]
			if t.Name == metadata.Product.Name {
				rows = [][]any{append(append([]any{}, p.Values...), metadata.Version, fetched, p.Hash)}
			}
			for _, row := range rows {
				args := append([]any{source, id}, row...)
				if _, err := w.insert[t.Name].ExecContext(ctx, args...); err != nil {
					return err
				}
			}
		}
	}
	warnings, err := json.Marshal(p.Warnings)
	if err != nil {
		return err
	}
	_, err = w.state.ExecContext(ctx, source, id, metadata.Version, fetched, status, p.Diagnostic(), string(warnings), now())
	return err
}

type MetadataSummary struct {
	Total    int `json:"total"`
	Current  int `json:"current"`
	Pending  int `json:"pending"`
	Warnings int `json:"warnings"`
	Failed   int `json:"failed"`
}

func (s *Store) RunMetadataCounts(ctx context.Context, runID string) (warnings, failed int, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT coalesce(sum(m.status='warnings'),0),coalesce(sum(m.status='failed'),0) FROM metadata_state m JOIN products p USING(source,product_id) WHERE p.last_success_run_id=? AND m.version=? AND m.response_fetched_at=p.fetched_at`, runID, metadata.Version).Scan(&warnings, &failed)
	return
}

type MetadataJob struct {
	Source    storefront.Source `json:"source"`
	Version   int               `json:"version"`
	State     string            `json:"state"`
	Total     int               `json:"total"`
	Processed int               `json:"processed"`
	Failed    int               `json:"failed"`
	Warned    int               `json:"warned"`
	LastID    int64             `json:"-"`
}

func (s *Store) MetadataSummary(ctx context.Context, source storefront.Source) (MetadataSummary, error) {
	var v MetadataSummary
	err := s.db.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(m.version=? AND m.response_fetched_at=p.fetched_at AND m.status IN ('complete','warnings')),0),coalesce(sum(m.version=? AND m.response_fetched_at=p.fetched_at AND m.status='warnings'),0),coalesce(sum(m.version=? AND m.response_fetched_at=p.fetched_at AND m.status='failed'),0) FROM products p LEFT JOIN metadata_state m USING(source,product_id) WHERE p.source=?`, metadata.Version, metadata.Version, metadata.Version, source).Scan(&v.Total, &v.Current, &v.Warnings, &v.Failed)
	v.Pending = v.Total - v.Current
	return v, err
}
func (s *Store) MetadataJob(ctx context.Context, source storefront.Source) (*MetadataJob, error) {
	j := MetadataJob{Source: source}
	err := s.db.QueryRowContext(ctx, `SELECT version,state,total,processed,failed,warned,last_id FROM metadata_jobs WHERE source=?`, source).Scan(&j.Version, &j.State, &j.Total, &j.Processed, &j.Failed, &j.Warned, &j.LastID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &j, err
}
func (s *Store) MaintenanceSource(ctx context.Context) (storefront.Source, error) {
	var source storefront.Source
	err := s.db.QueryRowContext(ctx, `SELECT source FROM metadata_jobs WHERE state IN ('running','paused','blocked') LIMIT 1`).Scan(&source)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return source, err
}
func (s *Store) BeginMetadata(ctx context.Context, source storefront.Source) (MetadataJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := MetadataJob{Source: source, Version: metadata.Version, State: "running"}
	if s.Version() != SchemaVersion {
		return j, errors.New("Apply the database update first")
	}
	var held int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM runs WHERE finished_at IS NULL`).Scan(&held); err != nil {
		return j, err
	}
	if held != 0 {
		return j, errors.New("End the unfinished crawl before starting maintenance")
	}
	if other, err := s.MaintenanceSource(ctx); err != nil {
		return j, err
	} else if other != "" && other != source {
		return j, errors.New("Finish the other storefront's maintenance first")
	}
	old, err := s.MetadataJob(ctx, source)
	if err != nil {
		return j, err
	}
	if old != nil && old.Version == metadata.Version && (old.State == "running" || old.State == "paused" || old.State == "blocked") {
		j = *old
		j.State = "running"
	} else {
		v, err := s.MetadataSummary(ctx, source)
		if err != nil {
			return j, err
		}
		j.Total = v.Pending
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO metadata_jobs VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(source) DO UPDATE SET version=excluded.version,state=excluded.state,total=excluded.total,processed=excluded.processed,failed=excluded.failed,warned=excluded.warned,last_id=excluded.last_id`, j.Source, j.Version, j.State, j.Total, j.Processed, j.Failed, j.Warned, j.LastID)
	return j, err
}
func (s *Store) SetMetadataState(ctx context.Context, source storefront.Source, state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `UPDATE metadata_jobs SET state=? WHERE source=?`, state, source)
	return err
}

type MetadataRow struct {
	ID      int64
	Fetched string
	JSON    []byte
}

func (s *Store) MetadataPage(ctx context.Context, source storefront.Source, after int64) ([]MetadataRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.product_id,p.fetched_at,p.response_json FROM products p LEFT JOIN metadata_state m USING(source,product_id) WHERE p.source=? AND p.product_id>? AND (m.version IS NULL OR m.version<>? OR m.response_fetched_at<>p.fetched_at OR m.status='failed') ORDER BY p.product_id LIMIT 64`, source, after, metadata.Version)
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
func (s *Store) CommitMetadata(ctx context.Context, j MetadataJob, rows []MetadataRow) (MetadataJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	original := j
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return original, err
	}
	defer tx.Rollback()
	w, err := prepareMetadata(ctx, tx)
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
			return original, errors.New("Metadata batch is not in product order")
		}
		var current string
		if err = check.QueryRowContext(ctx, j.Source, r.ID).Scan(&current); err != nil {
			return original, err
		}
		if current != r.Fetched {
			return original, errors.New("Product changed during metadata extraction")
		}
		projection := metadata.Extract(r.JSON, r.ID)
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
	updated, err := tx.ExecContext(ctx, `UPDATE metadata_jobs SET processed=?,failed=?,warned=?,last_id=? WHERE source=? AND state='running' AND version=? AND last_id=?`, j.Processed, j.Failed, j.Warned, j.LastID, j.Source, j.Version, original.LastID)
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

type MetadataIssue struct {
	ID       int64    `json:"id"`
	Status   string   `json:"status"`
	Message  string   `json:"message"`
	Warnings []string `json:"warnings"`
}

func (s *Store) MetadataIssues(ctx context.Context, source storefront.Source, offset int) ([]MetadataIssue, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.product_id,m.status,m.message,m.warnings_json FROM metadata_state m JOIN products p USING(source,product_id) WHERE m.source=? AND m.version=? AND m.response_fetched_at=p.fetched_at AND m.status IN ('warnings','failed') ORDER BY m.product_id LIMIT 50 OFFSET ?`, source, metadata.Version, max(0, offset))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MetadataIssue{}
	for rows.Next() {
		var r MetadataIssue
		var raw string
		if err = rows.Scan(&r.ID, &r.Status, &r.Message, &raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &r.Warnings); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
