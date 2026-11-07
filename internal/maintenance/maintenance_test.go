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
	m, err := New(ctx, s, storefront.DTRPG)
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
	m, err = New(ctx, s, storefront.DTRPG)
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
	m, err := New(ctx, s, storefront.DTRPG)
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
	m, err := New(ctx, s, storefront.DMSGuild)
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
