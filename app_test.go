package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/Rainbows4Blood/Nekonomicon/internal/auth"
	"github.com/Rainbows4Blood/Nekonomicon/internal/dtrpg"
	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

func TestExplicitUpgradeKeepsMaintenanceOnUnfinishedStorefront(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateRun(store.Run{ID: "guild", Source: storefront.DMSGuild, Mode: "new", Phase: "fetch", State: "paused"}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	db, err := sql.Open("sqlite", filepath.Join(dir, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA user_version=2"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	a := &App{source: storefront.DTRPG}
	if err = a.openCatalog(context.Background(), dir, false); err != nil {
		t.Fatal(err)
	}
	defer a.shutdown(context.Background())
	if a.Snapshot().Source != storefront.DMSGuild || a.engine != nil {
		t.Fatal("opening failed to inspect source/schema")
	}
	if err = a.SelectSource("dtrpg"); err != nil {
		t.Fatal(err)
	}
	if err = a.StartMaintenance("schema"); err != nil {
		t.Fatal(err)
	}
	a.maintenance.Wait(context.Background())
	if a.Snapshot().Source != storefront.DMSGuild || a.network.Source() != storefront.DMSGuild {
		t.Fatal("upgrade used mismatched storefront")
	}
	if err = a.End(); err != nil {
		t.Fatal(err)
	}
	if err = a.StartMaintenance("metadata"); err != nil {
		t.Fatal(err)
	}
	a.maintenance.Wait(context.Background())
	if a.Snapshot().Maintenance.Job.Source != storefront.DMSGuild {
		t.Fatal("maintenance retained pre-upgrade storefront")
	}
}

func TestSourceSwitchAndUnfinishedCatalogSelection(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := &App{source: storefront.DTRPG, network: dtrpg.NewNetwork(auth.Cookies{}, nil)}
	if err := a.openCatalog(context.Background(), t.TempDir(), false); err != nil {
		t.Fatal(err)
	}
	defer func() { a.store.Close() }()
	if err := a.StartMaintenance("schema"); err != nil {
		t.Fatal(err)
	}
	if err := a.maintenance.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.engine.SetReady(true); err != nil {
		t.Fatal(err)
	}
	if err := a.SelectSource("dmsguild"); err != nil {
		t.Fatal(err)
	}
	v := a.Snapshot()
	if v.Source != storefront.DMSGuild || v.ConnectionReady || v.ProbeID != 170778 || a.vault.Source != storefront.DMSGuild {
		t.Fatal("source switch retained old connection", v)
	}
	if err := a.SelectSource("../unknown"); err == nil {
		t.Fatal("unknown source accepted")
	}
	r := store.Run{ID: "guild", Source: storefront.DMSGuild, Mode: "refresh", Phase: "fetch", State: "paused"}
	if err := a.store.CreateRun(r); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.Commit(context.Background(), r.ID, []store.Result{{ID: 42, JSON: []byte(`{"productId":42}`)}}); err != nil {
		t.Fatal(err)
	}
	dir := a.folder
	a.store.Close()
	a.store, a.engine = nil, nil
	// An unfinished run overrides the previously selected storefront at startup.
	a.source = storefront.DTRPG
	a.network = dtrpg.NewNetwork(auth.Cookies{}, nil)
	if err := a.openCatalog(context.Background(), dir, false); err != nil {
		t.Fatal(err)
	}
	v = a.Snapshot()
	if v.Source != storefront.DMSGuild || v.Run == nil || v.Catalogued != 1 || v.ConnectionReady {
		t.Fatal("wrong source on reopen", v)
	}
	if err := a.SelectSource("dtrpg"); err == nil {
		t.Fatal("switched away from unfinished run")
	}
	if err := a.End(); err != nil {
		t.Fatal(err)
	}
	if err := a.SelectSource("dtrpg"); err != nil {
		t.Fatal(err)
	}
	if a.Snapshot().Catalogued != 0 {
		t.Fatal("counts leaked across storefronts")
	}
}
