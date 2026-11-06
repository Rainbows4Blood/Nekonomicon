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
