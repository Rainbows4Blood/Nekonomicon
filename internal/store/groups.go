package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Rainbows4Blood/Nekonomicon/internal/groups"
	"github.com/Rainbows4Blood/Nekonomicon/internal/metadata"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

type GroupsSummary = MetadataSummary
type GroupsJob = MetadataJob

func groupsSchema() string {
	q := `CREATE TABLE IF NOT EXISTS catalog_groups(id TEXT PRIMARY KEY,name TEXT NOT NULL,version INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS group_state(source TEXT NOT NULL,product_id INTEGER NOT NULL,version INTEGER NOT NULL,response_fetched_at TEXT NOT NULL,status TEXT NOT NULL CHECK(status IN ('complete','warnings','failed')),message TEXT NOT NULL,warnings_json TEXT NOT NULL,processed_at TEXT NOT NULL,official INTEGER CHECK(official IN (0,1)),official_reason TEXT NOT NULL,PRIMARY KEY(source,product_id),FOREIGN KEY(source,product_id) REFERENCES products(source,product_id));
 CREATE TABLE IF NOT EXISTS product_groups(group_id TEXT NOT NULL REFERENCES catalog_groups(id),source TEXT NOT NULL,product_id INTEGER NOT NULL,official INTEGER CHECK(official IN (0,1)),evidence TEXT NOT NULL,PRIMARY KEY(group_id,source,product_id),FOREIGN KEY(source,product_id) REFERENCES products(source,product_id));
 CREATE INDEX IF NOT EXISTS product_groups_product ON product_groups(source,product_id);
 CREATE INDEX IF NOT EXISTS product_groups_official ON product_groups(group_id,source,official,product_id);
 CREATE TABLE IF NOT EXISTS group_jobs(source TEXT PRIMARY KEY,version INTEGER NOT NULL,state TEXT NOT NULL,total INTEGER NOT NULL,processed INTEGER NOT NULL,failed INTEGER NOT NULL,warned INTEGER NOT NULL,last_id INTEGER NOT NULL);
 CREATE VIEW IF NOT EXISTS current_product_groups AS SELECT g.*,s.official_reason FROM product_groups g JOIN group_state s USING(source,product_id) JOIN products p USING(source,product_id) WHERE s.response_fetched_at=p.fetched_at AND s.status IN ('complete','warnings') AND s.version=` + fmt.Sprint(groups.Version) + `;
 `
	for _, d := range groups.Definitions {
		q += fmt.Sprintf("INSERT INTO catalog_groups VALUES('%s','%s',%d) ON CONFLICT(id) DO UPDATE SET name=excluded.name,version=excluded.version;", d.ID, d.Name, groups.Version)
	}
	return q
}

type groupsWriter struct{ clear, insert, state *sql.Stmt }

func prepareGroups(ctx context.Context, tx *sql.Tx) (*groupsWriter, error) {
	w := &groupsWriter{}
	var err error
	w.clear, err = tx.PrepareContext(ctx, "DELETE FROM product_groups WHERE source=? AND product_id=?")
	if err != nil {
		return nil, err
	}
	w.insert, err = tx.PrepareContext(ctx, "INSERT INTO product_groups VALUES(?,?,?,?,?)")
	if err != nil {
		w.close()
		return nil, err
	}
	w.state, err = tx.PrepareContext(ctx, `INSERT INTO group_state VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(source,product_id) DO UPDATE SET version=excluded.version,response_fetched_at=excluded.response_fetched_at,status=excluded.status,message=excluded.message,warnings_json=excluded.warnings_json,processed_at=excluded.processed_at,official=excluded.official,official_reason=excluded.official_reason`)
	if err != nil {
		w.close()
		return nil, err
	}
	return w, nil
}
func (w *groupsWriter) close() {
	for _, s := range []*sql.Stmt{w.clear, w.insert, w.state} {
		if s != nil {
			s.Close()
		}
	}
}
func (w *groupsWriter) write(ctx context.Context, source storefront.Source, id int64, fetched string, r groups.Result) error {
	status, msg := "complete", ""
	var official any
	if r.Official != nil {
		official = *r.Official
	}
	if r.Failure != "" {
		status, msg = "failed", r.Failure
	} else {
		if len(r.Warnings) > 0 {
			status, msg = "warnings", "Some grouping evidence is ambiguous; review the saved reasons"
		}
		if _, err := w.clear.ExecContext(ctx, source, id); err != nil {
			return err
		}
		for _, match := range r.Matches {
			if _, err := w.insert.ExecContext(ctx, match.Group, source, id, official, match.Evidence); err != nil {
				return err
			}
		}
	}
	warnings, err := json.Marshal(r.Warnings)
	if err != nil {
		return err
	}
	_, err = w.state.ExecContext(ctx, source, id, groups.Version, fetched, status, msg, string(warnings), now(), official, r.OfficialReason)
	return err
}
func (s *Store) GroupsSummary(ctx context.Context, source storefront.Source) (GroupsSummary, error) {
	var v GroupsSummary
	err := s.db.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(m.version=? AND m.response_fetched_at=p.fetched_at AND m.status IN ('complete','warnings')),0),coalesce(sum(m.version=? AND m.response_fetched_at=p.fetched_at AND m.status='warnings'),0),coalesce(sum(m.version=? AND m.response_fetched_at=p.fetched_at AND m.status='failed'),0) FROM products p LEFT JOIN group_state m USING(source,product_id) WHERE p.source=?`, groups.Version, groups.Version, groups.Version, source).Scan(&v.Total, &v.Current, &v.Warnings, &v.Failed)
	v.Pending = v.Total - v.Current
	return v, err
}
func (s *Store) GroupsJob(ctx context.Context, source storefront.Source) (*GroupsJob, error) {
	j := GroupsJob{Source: source}
	err := s.db.QueryRowContext(ctx, `SELECT version,state,total,processed,failed,warned,last_id FROM group_jobs WHERE source=?`, source).Scan(&j.Version, &j.State, &j.Total, &j.Processed, &j.Failed, &j.Warned, &j.LastID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &j, err
}
func (s *Store) BeginGroups(ctx context.Context, source storefront.Source) (GroupsJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := GroupsJob{Source: source, Version: groups.Version, State: "running"}
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
	var otherTask int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM metadata_jobs WHERE state IN ('running','paused','blocked')").Scan(&otherTask); err != nil {
		return j, err
	}
	if otherTask > 0 {
		return j, errors.New("Finish metadata maintenance first")
	}
	old, err := s.GroupsJob(ctx, source)
	if err != nil {
		return j, err
	}
	if old != nil && old.Version == groups.Version && (old.State == "running" || old.State == "paused" || old.State == "blocked") {
		j = *old
		j.State = "running"
	} else {
		v, err := s.GroupsSummary(ctx, source)
		if err != nil {
			return j, err
		}
		j.Total = v.Pending
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO group_jobs VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(source) DO UPDATE SET version=excluded.version,state=excluded.state,total=excluded.total,processed=excluded.processed,failed=excluded.failed,warned=excluded.warned,last_id=excluded.last_id`, j.Source, j.Version, j.State, j.Total, j.Processed, j.Failed, j.Warned, j.LastID)
	return j, err
}
func (s *Store) SetGroupsState(ctx context.Context, source storefront.Source, state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `UPDATE group_jobs SET state=? WHERE source=?`, state, source)
	return err
}

func (s *Store) GroupsPage(ctx context.Context, source storefront.Source, after int64) ([]MetadataRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.product_id,p.fetched_at,p.response_json FROM products p LEFT JOIN group_state m USING(source,product_id) WHERE p.source=? AND p.product_id>? AND (m.version IS NULL OR m.version<>? OR m.response_fetched_at<>p.fetched_at OR m.status='failed') ORDER BY p.product_id LIMIT 64`, source, after, groups.Version)
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
func (s *Store) CommitGroups(ctx context.Context, j GroupsJob, rows []MetadataRow) (GroupsJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	original := j
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return original, err
	}
	defer tx.Rollback()
	w, err := prepareGroups(ctx, tx)
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
			return original, errors.New("Group batch is not in product order")
		}
		var current string
		if err = check.QueryRowContext(ctx, j.Source, r.ID).Scan(&current); err != nil {
			return original, err
		}
		if current != r.Fetched {
			return original, errors.New("Product changed during grouping")
		}
		projection := groups.Derive(j.Source, metadata.Extract(r.JSON, r.ID))
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
	updated, err := tx.ExecContext(ctx, `UPDATE group_jobs SET processed=?,failed=?,warned=?,last_id=? WHERE source=? AND state='running' AND version=? AND last_id=?`, j.Processed, j.Failed, j.Warned, j.LastID, j.Source, j.Version, original.LastID)
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

func (s *Store) GroupsIssues(ctx context.Context, source storefront.Source, offset int) ([]MetadataIssue, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.product_id,m.status,m.message,m.warnings_json FROM group_state m JOIN products p USING(source,product_id) WHERE m.source=? AND m.version=? AND m.response_fetched_at=p.fetched_at AND m.status IN ('warnings','failed') ORDER BY m.product_id LIMIT 50 OFFSET ?`, source, groups.Version, max(0, offset))
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

// GroupMembers reads an indexed group range, then point-checks freshness. It
// never reads response_json or scans the product catalog. Empty source returns
// both stores; their product identities remain distinct. Cursor order is source/ID.
type GroupMember struct {
	Source         storefront.Source `json:"source"`
	ID             int64             `json:"id"`
	Official       *bool             `json:"official"`
	OfficialReason string            `json:"officialReason"`
	Evidence       string            `json:"evidence"`
}

func (s *Store) GroupMembers(ctx context.Context, group string, source storefront.Source, official *bool, afterSource storefront.Source, afterID int64) ([]GroupMember, error) {
	q := `SELECT g.source,g.product_id,g.official,g.official_reason,g.evidence FROM current_product_groups g WHERE g.group_id=?`
	args := []any{group}
	if source != "" {
		q += " AND g.source=?"
		args = append(args, source)
	}
	if official != nil {
		q += " AND g.official=?"
		args = append(args, *official)
	}
	q += " AND (g.source,g.product_id)>(?,?) ORDER BY g.source,g.product_id LIMIT 50"
	args = append(args, afterSource, afterID)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GroupMember{}
	for rows.Next() {
		var r GroupMember
		if err = rows.Scan(&r.Source, &r.ID, &r.Official, &r.OfficialReason, &r.Evidence); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
