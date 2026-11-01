// Package store owns every catalog write. Network and file operations must be
// finished before entering a transaction here.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gofrs/flock"
	_ "modernc.org/sqlite"
)

type Store struct {
	db   *sql.DB
	lock *flock.Flock
	mu   sync.Mutex
	Dir  string
}
type Run struct {
	ID       string  `json:"id"`
	Mode     string  `json:"mode"`
	Phase    string  `json:"phase"`
	State    string  `json:"state"`
	Started  string  `json:"started"`
	Finished *string `json:"finished"`
	Covers   bool    `json:"covers"`
}
type Failure struct {
	ID        int64  `json:"id"`
	Phase     string `json:"phase"`
	RunID     string `json:"runId"`
	Kind      string `json:"kind"`
	Status    int    `json:"status"`
	Message   string `json:"message"`
	Attempted string `json:"attempted"`
}
type Cover struct{ URL, Path string }
type Result struct {
	ID      int64
	JSON    []byte
	Cover   *Cover
	Failure *Failure
}
type Commit struct {
	Products, Added, Covers, Failures int
	Duration                          time.Duration
}
type Stats struct{ Products, ProductFailures, CoverFailures int }
type CoverRow struct {
	ID        int64
	JSON      []byte
	URL, Path string
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

const schema = `
CREATE TABLE IF NOT EXISTS runs(id TEXT PRIMARY KEY,mode TEXT NOT NULL CHECK(mode IN ('new','refresh','retry','covers')),phase TEXT NOT NULL,state TEXT NOT NULL,started_at TEXT NOT NULL,finished_at TEXT,download_covers INTEGER NOT NULL);
CREATE UNIQUE INDEX IF NOT EXISTS one_active_run ON runs((1)) WHERE finished_at IS NULL;
CREATE TABLE IF NOT EXISTS products(source TEXT NOT NULL CHECK(source='dtrpg'),product_id INTEGER NOT NULL CHECK(product_id>0),fetched_at TEXT NOT NULL,last_success_run_id TEXT NOT NULL REFERENCES runs(id),response_json TEXT NOT NULL CHECK(json_valid(response_json)),PRIMARY KEY(source,product_id));
CREATE TABLE IF NOT EXISTS failures(source TEXT NOT NULL CHECK(source='dtrpg'),product_id INTEGER NOT NULL CHECK(product_id>0),phase TEXT NOT NULL CHECK(phase IN ('product','cover')),run_id TEXT NOT NULL REFERENCES runs(id),kind TEXT NOT NULL,http_status INTEGER,message TEXT NOT NULL,attempted_at TEXT NOT NULL,PRIMARY KEY(source,product_id,phase));
CREATE TABLE IF NOT EXISTS covers(source TEXT NOT NULL CHECK(source='dtrpg'),product_id INTEGER NOT NULL CHECK(product_id>0),source_url TEXT NOT NULL,relative_path TEXT NOT NULL,downloaded_at TEXT NOT NULL,PRIMARY KEY(source,product_id));
PRAGMA user_version=1;`

func Open(dir string) (*Store, error) {
	dir, e := filepath.Abs(dir)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	lock := flock.New(filepath.Join(dir, "writer.lock"))
	ok, e := lock.TryLock()
	if e != nil || !ok {
		return nil, errors.New("Data folder is already in use or cannot be locked")
	}
	db, e := sql.Open("sqlite", filepath.Join(dir, "catalog.sqlite"))
	if e != nil {
		lock.Close()
		return nil, e
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, lock: lock, Dir: dir}
	for _, q := range []string{"PRAGMA busy_timeout=5000", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=NORMAL", "PRAGMA foreign_keys=ON"} {
		if _, e = db.Exec(q); e != nil {
			s.Close()
			return nil, e
		}
	}
	var version int
	if e = db.QueryRow("PRAGMA user_version").Scan(&version); e != nil || version > 1 {
		s.Close()
		return nil, errors.New("Catalog schema is newer than this application")
	}
	if _, e = db.Exec(schema); e != nil {
		s.Close()
		return nil, e
	}
	return s, nil
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.db.Close()
	if err := s.lock.Close(); e == nil {
		e = err
	}
	return e
}
func (s *Store) CreateRun(r Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, e := s.db.Exec(`INSERT INTO runs VALUES(?,?,?,?,?,NULL,?)`, r.ID, r.Mode, r.Phase, r.State, r.Started, r.Covers)
	return e
}
func (s *Store) UpdateRun(id, phase, state string, finished bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var at any
	if finished {
		at = now()
	}
	_, e := s.db.Exec(`UPDATE runs SET phase=?,state=?,finished_at=? WHERE id=?`, phase, state, at, id)
	return e
}
func (s *Store) Active() (*Run, error) {
	var r Run
	e := s.db.QueryRow(`SELECT id,mode,phase,state,started_at,finished_at,download_covers FROM runs WHERE finished_at IS NULL`).Scan(&r.ID, &r.Mode, &r.Phase, &r.State, &r.Started, &r.Finished, &r.Covers)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	return &r, e
}
func (s *Store) Stats() (Stats, error) {
	var v Stats
	e := s.db.QueryRow(`SELECT (SELECT count(*) FROM products),(SELECT count(*) FROM failures WHERE phase='product'),(SELECT count(*) FROM failures WHERE phase='cover')`).Scan(&v.Products, &v.ProductFailures, &v.CoverFailures)
	return v, e
}
func (s *Store) RunCounts(id string) (saved, failed int, err error) {
	err = s.db.QueryRow(`SELECT (SELECT count(*) FROM products WHERE last_success_run_id=?),(SELECT count(*) FROM failures WHERE run_id=?)`, id, id).Scan(&saved, &failed)
	return
}
func (s *Store) Eligibility(run Run, ids []int64) ([]int64, error) {
	// This read stores only IDs and markers, never response bodies.
	rows, e := s.db.Query(`SELECT product_id,last_success_run_id FROM products`)
	if e != nil {
		return nil, e
	}
	saved := map[int64]string{}
	for rows.Next() {
		var id int64
		var marker string
		if e = rows.Scan(&id, &marker); e != nil {
			break
		}
		saved[id] = marker
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return nil, e
	}
	rows, e = s.db.Query(`SELECT product_id,run_id,kind FROM failures WHERE phase='product' ORDER BY product_id`)
	if e != nil {
		return nil, e
	}
	skip := map[int64]bool{}
	var retry []int64
	for rows.Next() {
		var id int64
		var rid, kind string
		if e = rows.Scan(&id, &rid, &kind); e != nil {
			break
		}
		retry = append(retry, id)
		if rid == run.ID && kind != "authentication" {
			skip[id] = true
		}
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return nil, e
	}
	if run.Mode == "retry" {
		ids = retry
	}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		marker, exists := saved[id]
		if marker == run.ID || skip[id] || run.Mode == "new" && exists {
			continue
		}
		out = append(out, id)
	}
	return out, nil
}
func (s *Store) Commit(ctx context.Context, runID string, batch []Result) (Commit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	start := time.Now()
	v := Commit{}
	if len(batch) == 0 {
		return v, nil
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return v, e
	}
	defer tx.Rollback()
	prepare := func(q string) *sql.Stmt {
		if e != nil {
			return nil
		}
		var stmt *sql.Stmt
		stmt, e = tx.PrepareContext(ctx, q)
		return stmt
	}
	product := prepare(`INSERT INTO products VALUES('dtrpg',?,?,?,?) ON CONFLICT(source,product_id) DO UPDATE SET fetched_at=excluded.fetched_at,last_success_run_id=excluded.last_success_run_id,response_json=excluded.response_json`)
	exists := prepare(`SELECT EXISTS(SELECT 1 FROM products WHERE source='dtrpg' AND product_id=?)`)
	cover := prepare(`INSERT INTO covers VALUES('dtrpg',?,?,?,?) ON CONFLICT(source,product_id) DO UPDATE SET source_url=excluded.source_url,relative_path=excluded.relative_path,downloaded_at=excluded.downloaded_at`)
	failure := prepare(`INSERT INTO failures VALUES('dtrpg',?,?,?,?,?,?,?) ON CONFLICT(source,product_id,phase) DO UPDATE SET run_id=excluded.run_id,kind=excluded.kind,http_status=excluded.http_status,message=excluded.message,attempted_at=excluded.attempted_at`)
	clearFailure := prepare(`DELETE FROM failures WHERE source='dtrpg' AND product_id=? AND phase=?`)
	if e != nil {
		return Commit{}, e
	}
	defer product.Close()
	defer exists.Close()
	defer cover.Close()
	defer failure.Close()
	defer clearFailure.Close()
	at := now()
	for _, r := range batch {
		if r.Failure != nil {
			f := r.Failure
			_, e = failure.ExecContext(ctx, r.ID, f.Phase, runID, f.Kind, f.Status, f.Message, at)
			v.Failures++
		} else if r.Cover != nil {
			_, e = cover.ExecContext(ctx, r.ID, r.Cover.URL, r.Cover.Path, at)
			if e == nil {
				_, e = clearFailure.ExecContext(ctx, r.ID, "cover")
			}
			v.Covers++
		} else {
			var has bool
			e = exists.QueryRowContext(ctx, r.ID).Scan(&has)
			if e == nil {
				_, e = product.ExecContext(ctx, r.ID, at, runID, string(r.JSON))
			}
			if e == nil {
				_, e = clearFailure.ExecContext(ctx, r.ID, "product")
			}
			v.Products++
			if !has {
				v.Added++
			}
		}
		if e != nil {
			return Commit{}, e
		}
	}
	if e = tx.Commit(); e != nil {
		return Commit{}, e
	}
	v.Duration = time.Since(start)
	return v, nil
}
func (s *Store) CoverPage(after int64) ([]CoverRow, error) {
	rows, e := s.db.Query(`SELECT p.product_id,p.response_json,coalesce(c.source_url,''),coalesce(c.relative_path,'') FROM products p LEFT JOIN covers c USING(source,product_id) WHERE p.product_id>? ORDER BY p.product_id LIMIT 256`, after)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []CoverRow
	size := 0
	for rows.Next() {
		var r CoverRow
		if e = rows.Scan(&r.ID, &r.JSON, &r.URL, &r.Path); e != nil {
			return nil, e
		}
		out = append(out, r)
		size += len(r.JSON)
		if size >= 32<<20 {
			break
		}
	}
	return out, rows.Err()
}
func (s *Store) Failures(offset, limit int) ([]Failure, error) {
	if offset < 0 {
		offset = 0
	}
	limit = min(max(limit, 1), 100)
	rows, e := s.db.Query(`SELECT product_id,phase,run_id,kind,coalesce(http_status,0),message,attempted_at FROM failures ORDER BY attempted_at DESC,product_id,phase LIMIT ? OFFSET ?`, limit, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Failure{}
	for rows.Next() {
		var f Failure
		if e = rows.Scan(&f.ID, &f.Phase, &f.RunID, &f.Kind, &f.Status, &f.Message, &f.Attempted); e != nil {
			return nil, e
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
func (s *Store) Response(id int64) ([]byte, error) {
	var b []byte
	e := s.db.QueryRow(`SELECT response_json FROM products WHERE source='dtrpg' AND product_id=?`, id).Scan(&b)
	return b, e
}
func (s *Store) String() string { return fmt.Sprintf("DTRPG catalog (%s)", s.Dir) }
