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
	"sync/atomic"
	"time"

	"github.com/Rainbows4Blood/Nekonomicon/internal/catalog"
	"github.com/Rainbows4Blood/Nekonomicon/internal/groups"
	"github.com/Rainbows4Blood/Nekonomicon/internal/metadata"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
	"github.com/gofrs/flock"
	_ "modernc.org/sqlite"
)

type Store struct {
	db      *sql.DB
	lock    *flock.Flock
	mu      sync.Mutex
	Dir     string
	version atomic.Int32
}
type Run struct {
	Source   storefront.Source `json:"source"`
	ID       string            `json:"id"`
	Mode     string            `json:"mode"`
	Phase    string            `json:"phase"`
	State    string            `json:"state"`
	Started  string            `json:"started"`
	Finished *string           `json:"finished"`
	Covers   bool              `json:"covers"`
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
	Products, Added, Covers, AddedCovers, Failures int
	MetadataWarnings, MetadataFailures             int
	Duration                                       time.Duration
}
type Stats struct{ Products, Covers, ProductFailures, CoverFailures int }
type CoverRow struct {
	ID        int64
	JSON      []byte
	URL, Path string
}

var ErrUpgrade = errors.New("Catalog upgrade could not finish. The existing catalog is unchanged; check free disk space and retry")

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

const schema = `
CREATE TABLE IF NOT EXISTS runs(id TEXT PRIMARY KEY,mode TEXT NOT NULL CHECK(mode IN ('new','refresh','retry','covers')),phase TEXT NOT NULL,state TEXT NOT NULL,started_at TEXT NOT NULL,finished_at TEXT,download_covers INTEGER NOT NULL,source TEXT NOT NULL DEFAULT 'dtrpg' CHECK(source IN ('dtrpg','dmsguild')));
CREATE UNIQUE INDEX IF NOT EXISTS one_active_run ON runs((1)) WHERE finished_at IS NULL;
CREATE TABLE IF NOT EXISTS products(source TEXT NOT NULL CHECK(source IN ('dtrpg','dmsguild')),product_id INTEGER NOT NULL CHECK(product_id>0),fetched_at TEXT NOT NULL,last_success_run_id TEXT NOT NULL REFERENCES runs(id),response_json TEXT NOT NULL CHECK(json_valid(response_json)),PRIMARY KEY(source,product_id));
CREATE TABLE IF NOT EXISTS failures(source TEXT NOT NULL CHECK(source IN ('dtrpg','dmsguild')),product_id INTEGER NOT NULL CHECK(product_id>0),phase TEXT NOT NULL CHECK(phase IN ('product','cover')),run_id TEXT NOT NULL REFERENCES runs(id),kind TEXT NOT NULL,http_status INTEGER,message TEXT NOT NULL,attempted_at TEXT NOT NULL,PRIMARY KEY(source,product_id,phase));
CREATE TABLE IF NOT EXISTS covers(source TEXT NOT NULL CHECK(source IN ('dtrpg','dmsguild')),product_id INTEGER NOT NULL CHECK(product_id>0),source_url TEXT NOT NULL,relative_path TEXT NOT NULL,downloaded_at TEXT NOT NULL,PRIMARY KEY(source,product_id));
`

func Open(dir string) (*Store, error) { return OpenContext(context.Background(), dir, nil) }

// OpenContext reports coarse migration stages without exposing catalog content.
// Cancellation waits for rollback and connection close before releasing the lock.
func OpenContext(ctx context.Context, dir string, progress func(string)) (*Store, error) {
	s, err := InspectContext(ctx, dir, nil)
	if err != nil {
		return nil, err
	}
	if err = s.Upgrade(ctx, progress); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// InspectContext opens and locks a catalog without creating a database or
// modifying its schema. Desktop startup uses this; upgrades require maintenance.
func InspectContext(ctx context.Context, dir string, progress func(string)) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir, e := filepath.Abs(dir)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	// Windows can redirect AppData for a process launched by a packaged app.
	// Resolve an actual catalog handle, not just the apparent directory name,
	// so the lock, cover files and displayed path refer to the same real folder.
	catalog, e := os.Open(filepath.Join(dir, "catalog.sqlite"))
	missing := errors.Is(e, os.ErrNotExist)
	if missing {
		catalog, e = os.Open(dir)
	}
	if e != nil {
		return nil, e
	}
	catalogPath, pathErr := physicalFilePath(catalog)
	closeErr := catalog.Close()
	if pathErr != nil {
		return nil, pathErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if missing {
		dir = catalogPath
		catalogPath = filepath.Join(dir, "catalog.sqlite")
	} else {
		dir = filepath.Dir(catalogPath)
	}
	lock := flock.New(filepath.Join(dir, "writer.lock"))
	ok, e := lock.TryLock()
	if e != nil || !ok {
		return nil, errors.New("Data folder is already in use or cannot be locked")
	}
	if missing {
		return &Store{lock: lock, Dir: dir}, nil
	}
	db, e := sql.Open("sqlite", catalogPath)
	if e != nil {
		lock.Close()
		return nil, e
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, lock: lock, Dir: dir}
	for _, q := range []string{"PRAGMA busy_timeout=5000", "PRAGMA synchronous=NORMAL", "PRAGMA foreign_keys=ON"} {
		if _, e = db.ExecContext(ctx, q); e != nil {
			s.Close()
			return nil, e
		}
	}
	var version int
	if e = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); e != nil {
		s.Close()
		return nil, e
	}
	s.version.Store(int32(version))
	return s, nil
}

const SchemaVersion = 9

func (s *Store) Version() int { return int(s.version.Load()) }
func (s *Store) Upgrade(ctx context.Context, progress func(string)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Version() > SchemaVersion {
		return errors.New("This catalog needs a newer version of Nekonomicon")
	}
	if s.Version() == SchemaVersion {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if s.db == nil {
		db, err := sql.Open("sqlite", filepath.Join(s.Dir, "catalog.sqlite"))
		if err != nil {
			return err
		}
		s.db = db
		db.SetMaxOpenConns(1)
	}
	for _, q := range []string{"PRAGMA busy_timeout=5000", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=NORMAL", "PRAGMA foreign_keys=ON"} {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	if err := migrate(ctx, s.db, s.Version(), progress); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrUpgrade
	}
	s.version.Store(SchemaVersion)
	return nil
}

// Rebuild only the tables whose v1 CHECK constraints fixed the source to DTRPG.
// DDL, response copies and version advancement commit atomically.
func migrate(ctx context.Context, db *sql.DB, version int, progress func(string)) error {
	report := func(message string) {
		if progress != nil {
			progress(message)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if version == 1 {
		report("Preparing your catalog upgrade…")
	}
	// Explicit rollback owns cleanup; do not race database/sql’s background
	// rollback on a cancelled BeginTx context. Each statement is cancellable.
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if version == 1 {
		if _, err = tx.ExecContext(ctx, "ALTER TABLE runs ADD COLUMN source TEXT NOT NULL DEFAULT 'dtrpg' CHECK(source IN ('dtrpg','dmsguild'))"); err != nil {
			return err
		}
		for _, table := range []string{"products", "failures", "covers"} {
			if _, err = tx.ExecContext(ctx, "ALTER TABLE "+table+" RENAME TO "+table+"_v1"); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, schema); err != nil {
		return err
	}
	if version == 1 {
		for _, table := range []string{"products", "failures", "covers"} {
			labels := map[string]string{"products": "Copying saved products…", "failures": "Preserving recorded errors…", "covers": "Preserving saved cover references…"}
			report(labels[table])
			if _, err = tx.ExecContext(ctx, "INSERT INTO "+table+" SELECT * FROM "+table+"_v1"); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "DROP TABLE "+table+"_v1"); err != nil {
				return err
			}
		}
	}
	if version == 1 {
		report("Finishing your catalog upgrade…")
	}
	if version < 3 {
		report("Preparing metadata tables…")
		if _, err = tx.ExecContext(ctx, metadataSchema()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "PRAGMA user_version=3"); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if version < 4 {
		report("Preparing D&D group tables…")
		if _, err = tx.ExecContext(ctx, groupsSchema()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "PRAGMA user_version=4"); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if version < 5 {
		report("Preparing D&D browsing and search indexes…")
		if _, err = tx.ExecContext(ctx, catalogSchema()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "PRAGMA user_version=5"); err != nil {
			return err
		}
	}
	if version < 6 {
		report("Indexing storefront filters and maintenance counts…")
		if _, err = tx.ExecContext(ctx, storefrontIndexSchema()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "PRAGMA user_version=6"); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	if version < 7 {
		report("Preparing adventure enrichment storage…")
		var hasAdventure int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('dnd_books') WHERE name='is_adventure'`).Scan(&hasAdventure); err != nil {
			return err
		}
		if hasAdventure == 0 {
			if _, err = tx.ExecContext(ctx, `ALTER TABLE dnd_books ADD COLUMN is_adventure INTEGER NOT NULL DEFAULT 0`); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, enrichmentSchema()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "PRAGMA user_version=7"); err != nil {
			return err
		}
	}
	if version < 8 {
		report("Recording completed enrichment independently of known values…")
		for _, column := range []string{"players", "levels"} {
			var exists int
			if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('enrichment_results') WHERE name=?`, column+"_done").Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				if _, err = tx.ExecContext(ctx, `ALTER TABLE enrichment_results ADD COLUMN `+column+`_done INTEGER NOT NULL DEFAULT 0`); err != nil {
					return err
				}
				// Legacy unknown results cannot distinguish a finished web pass from
				// an interrupted storefront checkpoint. Preserve known completions;
				// allow one follow-up for ambiguous legacy results.
				if _, err = tx.ExecContext(ctx, `UPDATE enrichment_results SET `+column+`_done=`+column+`_complete`); err != nil {
					return err
				}
			}
		}
		if _, err = tx.ExecContext(ctx, "PRAGMA user_version=8"); err != nil {
			return err
		}
	}
	if version < 9 {
		report("Adding setting, location and in-world date enrichments…")
		for _, column := range []string{"settings", "locations", "dates"} {
			for _, field := range []struct{ name, definition string }{{"json", "TEXT NOT NULL DEFAULT '{}'"}, {"done", "INTEGER NOT NULL DEFAULT 0"}, {"complete", "INTEGER NOT NULL DEFAULT 0"}} {
				var exists int
				name := column + "_" + field.name
				if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('enrichment_results') WHERE name=?`, name).Scan(&exists); err != nil {
					return err
				}
				if exists == 0 {
					if _, err = tx.ExecContext(ctx, `ALTER TABLE enrichment_results ADD COLUMN `+name+` `+field.definition); err != nil {
						return err
					}
				}
			}
		}
		// Preserve both old completion markers, including completed unknowns.
		// Only the three newly introduced enrichments become pending.
		if _, err = tx.ExecContext(ctx, `UPDATE enrichment_results SET status='partial' WHERE status='complete' AND (settings_done=0 OR locations_done=0 OR dates_done=0)`); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "PRAGMA user_version=9"); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var e error
	if s.db != nil {
		e = s.db.Close()
	}
	if err := s.lock.Close(); e == nil {
		e = err
	}
	return e
}
func (s *Store) CreateRun(r Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Version() != SchemaVersion {
		return errors.New("Database needs maintenance before crawling")
	}
	var held int
	if err := s.db.QueryRow(`SELECT (SELECT count(*) FROM metadata_jobs WHERE state IN ('running','paused','blocked'))+(SELECT count(*) FROM group_jobs WHERE state IN ('running','paused','blocked'))+(SELECT count(*) FROM catalog_jobs WHERE state IN ('running','paused','blocked'))`).Scan(&held); err != nil {
		return err
	}
	if held > 0 {
		return errors.New("Finish or end data maintenance before crawling")
	}
	_, e := s.db.Exec(`INSERT INTO runs(id,mode,phase,state,started_at,finished_at,download_covers,source) VALUES(?,?,?,?,?,NULL,?,?)`, r.ID, r.Mode, r.Phase, r.State, r.Started, r.Covers, storefront.Default(r.Source))
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
func (s *Store) Active() (*Run, error) { return s.ActiveContext(context.Background()) }
func (s *Store) ActiveContext(ctx context.Context) (*Run, error) {
	var r Run
	e := s.db.QueryRowContext(ctx, `SELECT id,mode,phase,state,started_at,finished_at,download_covers,source FROM runs WHERE finished_at IS NULL`).Scan(&r.ID, &r.Mode, &r.Phase, &r.State, &r.Started, &r.Finished, &r.Covers, &r.Source)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	return &r, e
}
func (s *Store) Stats(sources ...storefront.Source) (Stats, error) {
	return s.StatsContext(context.Background(), sources...)
}
func (s *Store) StatsContext(ctx context.Context, sources ...storefront.Source) (Stats, error) {
	var v Stats
	e := s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM products WHERE source=?),(SELECT count(*) FROM covers WHERE source=?),(SELECT count(*) FROM failures WHERE phase='product' AND source=?),(SELECT count(*) FROM failures WHERE phase='cover' AND source=?)`, storefront.Default(sources...), storefront.Default(sources...), storefront.Default(sources...), storefront.Default(sources...)).Scan(&v.Products, &v.Covers, &v.ProductFailures, &v.CoverFailures)
	return v, e
}
func (s *Store) RunCounts(id string) (saved, failed int, err error) {
	return s.RunCountsContext(context.Background(), id)
}
func (s *Store) RunCountsContext(ctx context.Context, id string) (saved, failed int, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM products WHERE last_success_run_id=?),(SELECT count(*) FROM failures WHERE run_id=? AND phase='product')`, id, id).Scan(&saved, &failed)
	return
}
func (s *Store) Eligibility(run Run, ids []int64) ([]int64, error) {
	// This read stores only IDs and markers, never response bodies.
	rows, e := s.db.Query(`SELECT product_id,last_success_run_id FROM products WHERE source=?`, storefront.Default(run.Source))
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
	rows, e = s.db.Query(`SELECT product_id,run_id,kind FROM failures WHERE phase='product' AND source=? ORDER BY product_id`, storefront.Default(run.Source))
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
	if s.Version() != SchemaVersion {
		return v, errors.New("Database needs maintenance before crawling")
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return v, e
	}
	defer tx.Rollback()
	mw, e := prepareMetadata(ctx, tx)
	if e != nil {
		return Commit{}, e
	}
	defer mw.close()
	gw, e := prepareGroups(ctx, tx)
	if e != nil {
		return Commit{}, e
	}
	defer gw.close()
	cw, e := prepareCatalog(ctx, tx)
	if e != nil {
		return Commit{}, e
	}
	defer cw.close()
	var source storefront.Source
	if e = tx.QueryRowContext(ctx, `SELECT source FROM runs WHERE id=?`, runID).Scan(&source); e != nil {
		return v, e
	}
	prepare := func(q string) *sql.Stmt {
		if e != nil {
			return nil
		}
		var stmt *sql.Stmt
		stmt, e = tx.PrepareContext(ctx, q)
		return stmt
	}
	product := prepare(`INSERT INTO products VALUES(?,?,?,?,?) ON CONFLICT(source,product_id) DO UPDATE SET fetched_at=excluded.fetched_at,last_success_run_id=excluded.last_success_run_id,response_json=excluded.response_json`)
	exists := prepare(`SELECT EXISTS(SELECT 1 FROM products WHERE source=? AND product_id=?)`)
	coverExists := prepare(`SELECT EXISTS(SELECT 1 FROM covers WHERE source=? AND product_id=?)`)
	cover := prepare(`INSERT INTO covers VALUES(?,?,?,?,?) ON CONFLICT(source,product_id) DO UPDATE SET source_url=excluded.source_url,relative_path=excluded.relative_path,downloaded_at=excluded.downloaded_at`)
	failure := prepare(`INSERT INTO failures VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(source,product_id,phase) DO UPDATE SET run_id=excluded.run_id,kind=excluded.kind,http_status=excluded.http_status,message=excluded.message,attempted_at=excluded.attempted_at`)
	clearFailure := prepare(`DELETE FROM failures WHERE source=? AND product_id=? AND phase=?`)
	if e != nil {
		return Commit{}, e
	}
	defer product.Close()
	defer exists.Close()
	defer coverExists.Close()
	defer cover.Close()
	defer failure.Close()
	defer clearFailure.Close()
	at := now()
	for _, r := range batch {
		if r.Failure != nil {
			f := r.Failure
			_, e = failure.ExecContext(ctx, source, r.ID, f.Phase, runID, f.Kind, f.Status, f.Message, at)
			v.Failures++
		} else if r.Cover != nil {
			var hasCover bool
			e = coverExists.QueryRowContext(ctx, source, r.ID).Scan(&hasCover)
			if e == nil {
				_, e = cover.ExecContext(ctx, source, r.ID, r.Cover.URL, r.Cover.Path, at)
			}
			if e == nil {
				_, e = clearFailure.ExecContext(ctx, source, r.ID, "cover")
			}
			v.Covers++
			if !hasCover {
				v.AddedCovers++
			}
		} else {
			// Project one response at a time, so a batch of small responses with
			// many repeated entries cannot accumulate unbounded derived rows.
			if e = ctx.Err(); e != nil {
				return Commit{}, e
			}
			projection := metadata.Extract(r.JSON, r.ID)
			var has bool
			e = exists.QueryRowContext(ctx, source, r.ID).Scan(&has)
			if e == nil {
				_, e = product.ExecContext(ctx, source, r.ID, at, runID, string(r.JSON))
			}
			if e == nil {
				_, e = clearFailure.ExecContext(ctx, source, r.ID, "product")
			}
			if e == nil {
				e = mw.write(ctx, source, r.ID, at, projection)
				if e == nil {
					e = gw.write(ctx, source, r.ID, at, groups.Derive(source, projection))
					if e == nil {
						e = cw.write(ctx, source, r.ID, at, catalog.Derive(source, r.ID, projection))
					}
				}
			}
			if projection.Failure != "" {
				v.MetadataFailures++
			} else if len(projection.Warnings) > 0 {
				v.MetadataWarnings++
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

// Constrain the leading key as well as the cursor so SQLite can seek directly
// into (source, product_id), without scanning and sorting the whole catalog.
const coverPageQuery = `SELECT p.product_id,p.response_json,coalesce(c.source_url,''),coalesce(c.relative_path,'') FROM products p LEFT JOIN covers c USING(source,product_id) WHERE p.source=? AND p.product_id>? ORDER BY p.product_id LIMIT 256`

func (s *Store) CoverPage(after int64, sources ...storefront.Source) ([]CoverRow, error) {
	rows, e := s.db.Query(coverPageQuery, storefront.Default(sources...), after)
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
func (s *Store) Failures(offset, limit int, sources ...storefront.Source) ([]Failure, error) {
	if offset < 0 {
		offset = 0
	}
	limit = min(max(limit, 1), 100)
	rows, e := s.db.Query(`SELECT product_id,phase,run_id,kind,coalesce(http_status,0),message,attempted_at FROM failures WHERE source=? ORDER BY attempted_at DESC,product_id,phase LIMIT ? OFFSET ?`, storefront.Default(sources...), limit, offset)
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
func (s *Store) Response(id int64, sources ...storefront.Source) ([]byte, error) {
	var b []byte
	e := s.db.QueryRow(`SELECT response_json FROM products WHERE source=? AND product_id=?`, storefront.Default(sources...), id).Scan(&b)
	return b, e
}
func (s *Store) String() string { return fmt.Sprintf("Local catalog (%s)", s.Dir) }
