package maintenance

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

func TestExplicitUpgradeExtractionWarningsAndRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dir := t.TempDir()
	s, err := store.InspectContext(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Snapshot().NeedsSchema || s.Version() != 0 {
		t.Fatal("initialization was automatic")
	}
	if err = m.Start("metadata", nil); err == nil {
		t.Fatal("extracted before explicit schema update")
	}
	if err = m.Start("schema", nil); err != nil {
		t.Fatal(err)
	}
	if err = m.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().NeedsSchema || m.Snapshot().State != "completed" {
		t.Fatal(m.Snapshot())
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO runs VALUES('seed','new','fetch','completed','now','now',0,'dtrpg');
WITH RECURSIVE ids(n) AS(SELECT 1 UNION ALL SELECT n+1 FROM ids WHERE n<130)
INSERT INTO products SELECT 'dtrpg',n,'original','seed',json_object('productId',n,'description',json_object('name','Book'),'pagecount','iv + 2') FROM ids;`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Summary.Pending != 130 {
		t.Fatal(m.Snapshot())
	}
	if err = m.Start("metadata", nil); err != nil {
		t.Fatal(err)
	}
	if err = m.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	v := m.Snapshot()
	if v.State != "completed_warnings" || v.Job.Processed != 130 || v.Job.Warned != 130 || v.Summary.Pending != 0 {
		t.Fatal(v)
	}
	s.Close()
	s, err = store.InspectContext(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m, err = New(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Running || m.Snapshot().Summary.Current != 130 {
		t.Fatal("restart did work or lost progress")
	}
	if err = m.Start("metadata", nil); err != nil {
		t.Fatal(err)
	}
	if err = m.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Job.Total != 0 || m.Snapshot().Job.Processed != 0 {
		t.Fatal("warning-only products processed again")
	}
}
func TestPauseAndShutdownSettleBeforeStoreClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	db, err := sql.Open("sqlite", filepath.Join(s.Dir, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO runs VALUES('seed','new','fetch','completed','now','now',0,'dtrpg'); WITH RECURSIVE ids(n) AS(SELECT 1 UNION ALL SELECT n+1 FROM ids WHERE n<1000) INSERT INTO products SELECT 'dtrpg',n,'original','seed',json_object('productId',n,'description',json_object('name','Book')) FROM ids;`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Start("metadata", nil); err != nil {
		t.Fatal(err)
	}
	m.Shutdown()
	if m.Snapshot().Running {
		t.Fatal("shutdown returned while running")
	}
	if err = m.Start("metadata", nil); err != nil {
		t.Fatal(err)
	}
	if err = m.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Summary.Current != 1000 || m.Snapshot().Summary.Pending != 0 {
		t.Fatal("resume skipped records", m.Snapshot())
	}
}
func TestGroupingManagerRestartAndTaskIdentity(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	db, err := sql.Open("sqlite", filepath.Join(s.Dir, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO runs VALUES('seed','new','fetch','completed','now','now',0,'dmsguild'); WITH RECURSIVE ids(n) AS(SELECT 1 UNION ALL SELECT n+1 FROM ids WHERE n<130) INSERT INTO products SELECT 'dmsguild',n,'original','seed',json_object('productId',n,'publisherId',8957,'filters',json_array(json_object('filterId',1000263))) FROM ids;`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.BeginGroups(ctx, storefront.DMSGuild)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.GroupsPage(ctx, storefront.DMSGuild, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CommitGroups(ctx, j, rows); err != nil {
		t.Fatal(err)
	}
	s.SetGroupsState(ctx, storefront.DMSGuild, "paused")
	m, err := New(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Action != "groups" || !m.Snapshot().Held {
		t.Fatal(m.Snapshot())
	}
	if err = m.Start("metadata", nil); err == nil {
		t.Fatal("switched a paused task")
	}
	if err = m.Start("groups", nil); err != nil {
		t.Fatal(err)
	}
	if err = m.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	v := m.Snapshot()
	if v.Action != "groups" || v.Job.Processed != 130 || v.Groups.Pending != 0 || v.Summary.Pending != 130 || v.Held {
		t.Fatal(v)
	}
	if err = m.Start("metadata", nil); err != nil {
		t.Fatal(err)
	}
	m.Shutdown()
}

// A failure at the storefront boundary must keep both cursors recoverable.
// Identical IDs belong to separate products and failed outcomes are counted once.
func TestDatabaseMaintenanceAcrossStorefrontsAndRestart(t *testing.T) {
	for _, action := range []string{"metadata", "groups", "catalog"} {
		t.Run(action, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			dir := t.TempDir()
			s, err := store.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { s.Close() }()
			db, err := sql.Open("sqlite", filepath.Join(dir, "catalog.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			_, err = db.Exec(`INSERT INTO runs VALUES('seed','new','fetch','completed','now','now',0,'dtrpg');
WITH RECURSIVE ids(n) AS(SELECT 1 UNION ALL SELECT n+1 FROM ids WHERE n<65), sources(s) AS(VALUES('dtrpg'),('dmsguild'))
INSERT INTO products SELECT s,n,'original','seed',json_object('productId',CASE WHEN n=1 THEN 999 ELSE n END,'description',json_object('name','Book'),'publisherId',8957,'filters',json_array(json_object('filterId',CASE WHEN s='dtrpg' THEN 44705 ELSE 1000263 END))) FROM ids CROSS JOIN sources;`)
			if err != nil {
				t.Fatal(err)
			}
			table := "metadata_state"
			if action == "groups" {
				table = "group_state"
			}
			if action == "catalog" {
				table = "catalog_state"
			}
			_, err = db.Exec("CREATE TRIGGER fail_second_store BEFORE INSERT ON " + table + " WHEN NEW.source='dmsguild' BEGIN SELECT RAISE(ABORT,'synthetic'); END")
			if err != nil {
				t.Fatal(err)
			}
			m, err := New(ctx, s)
			if err != nil {
				t.Fatal(err)
			}
			if m.Snapshot().Summary.Total != 130 || m.Snapshot().Groups.Total != 130 {
				t.Fatal(m.Snapshot())
			}
			if err = m.Start(action, nil); err != nil {
				t.Fatal(err)
			}
			if err = m.Wait(ctx); err != nil {
				t.Fatal(err)
			}
			v := m.Snapshot()
			if v.State != "blocked" || !v.Held || v.Job.Total != 130 || v.Job.Processed != 65 || v.Job.Failed != 1 {
				t.Fatal(v)
			}
			if err = s.CreateRun(store.Run{ID: "blocked", Mode: "new"}); err == nil {
				t.Fatal("crawl overlapped database maintenance")
			}
			if _, err = db.Exec("DROP TRIGGER fail_second_store"); err != nil {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = store.InspectContext(ctx, dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			m, err = New(ctx, s)
			if err != nil {
				t.Fatal(err)
			}
			v = m.Snapshot()
			if v.Running || !v.Held || v.Action != action || v.Job.Processed != 65 {
				t.Fatal("restart changed committed work", v)
			}
			other := "metadata"
			if action == other {
				other = "groups"
			}
			if err = m.Start(other, nil); err == nil {
				t.Fatal("replaced paused task")
			}
			if err = m.Start(action, nil); err != nil {
				t.Fatal(err)
			}
			if err = m.Wait(ctx); err != nil {
				t.Fatal(err)
			}
			v = m.Snapshot()
			summary, issues := v.Summary, s.MetadataIssues
			if action == "groups" {
				summary, issues = v.Groups, s.GroupsIssues
			}
			if action == "catalog" {
				summary, issues = v.Catalog, s.CatalogIssues
			}
			if v.Held || v.State != "completed_warnings" || v.Job.Processed != 130 || v.Job.Failed != 2 || summary.Current != 128 || summary.Pending != 2 {
				t.Fatal(v, summary)
			}
			rows, err := issues(ctx, "", 0)
			if err != nil || len(rows) != 2 || rows[0].Source != storefront.DMSGuild || rows[1].Source != storefront.DTRPG || rows[0].ID != 1 || rows[1].ID != 1 {
				t.Fatal(rows, err)
			}
			rows, err = issues(ctx, "", 1)
			if err != nil || len(rows) != 1 || rows[0].Source != storefront.DTRPG {
				t.Fatal("unstable issue paging", rows, err)
			}
			// End releases every storefront checkpoint; a new pass retries only failures.
			jobs, err := s.BeginDatabaseMaintenance(ctx, action)
			if err != nil || len(jobs) != 2 || jobs[0].Total != 1 || jobs[1].Total != 1 {
				t.Fatal(jobs, err)
			}
			m, err = New(ctx, s)
			if err != nil {
				t.Fatal(err)
			}
			if err = m.End(); err != nil {
				t.Fatal(err)
			}
			if err = s.CreateRun(store.Run{ID: "released", Mode: "new"}); err != nil {
				t.Fatal("end retained maintenance ownership", err)
			}
			var unchanged int
			if err = db.QueryRow("SELECT count(*) FROM products WHERE fetched_at='original' AND last_success_run_id='seed'").Scan(&unchanged); err != nil || unchanged != 130 {
				t.Fatal("maintenance changed originals", unchanged, err)
			}
		})
	}
}

func TestSchemaUpgradeRestoresPausedGrouping(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.BeginGroups(ctx, storefront.DMSGuild); err != nil {
		t.Fatal(err)
	}
	if err = s.SetGroupsState(ctx, storefront.DMSGuild, "paused"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	db, err := sql.Open("sqlite", filepath.Join(dir, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA user_version=4"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err = store.InspectContext(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m, err := New(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Start("schema", nil); err != nil {
		t.Fatal(err)
	}
	if err = m.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if v := m.Snapshot(); v.NeedsSchema || !v.Held || v.Action != "groups" || v.State != "paused" {
		t.Fatal("upgrade hid recoverable task", v)
	}
	if err = m.Start("catalog", nil); err == nil {
		t.Fatal("replaced unfinished task after upgrade")
	}
	if err = m.Start("groups", nil); err != nil {
		t.Fatal(err)
	}
	if err = m.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}
