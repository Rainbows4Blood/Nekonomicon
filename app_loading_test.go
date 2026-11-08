package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
)

func loadingFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	b, err := os.ReadFile("internal/store/testdata/v1.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(b)); err != nil {
		t.Fatal(err)
	}
	return dir
}

func waitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for catalog lifecycle")
	}
}

func TestLoadingRemainsResponsiveAndCloseWaitsForRollback(t *testing.T) {
	for _, windowClose := range []bool{false, true} {
		t.Run(map[bool]string{false: "abort button", true: "window close"}[windowClose], func(t *testing.T) {
			t.Setenv("APPDATA", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			dir := loadingFixture(t)
			if err := savePreferences(dir, "dtrpg"); err != nil {
				t.Fatal(err)
			}
			entered, cancelled, settle, quit := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			a := &App{quit: func(context.Context) { close(quit) }}
			a.openStore = func(ctx context.Context, folder string, report func(string)) (*store.Store, error) {
				return store.OpenContext(ctx, folder, func(message string) {
					report(message)
					if message == "Copying saved products…" {
						close(entered)
						<-ctx.Done()
						close(cancelled)
						<-settle
					}
				})
			}
			a.startup(context.Background())
			defer a.shutdown(context.Background())
			defer close(settle)
			waitSignal(t, entered)
			view := make(chan View, 1)
			go func() { view <- a.Snapshot() }()
			select {
			case v := <-view:
				if !v.Loading || !v.Migrating || v.StorageReady {
					t.Fatal("incorrect loading state", v)
				}
			case <-time.After(time.Second):
				t.Fatal("snapshot blocked by migration")
			}
			if err := a.Start("new", true); err == nil {
				t.Fatal("started during migration")
			}
			if err := a.SelectSource("dmsguild"); err == nil {
				t.Fatal("switched during migration")
			}
			if err := a.SaveCookies("synthetic=value", ""); err == nil {
				t.Fatal("changed cookies during migration")
			}
			if windowClose {
				if !a.beforeClose(context.Background()) {
					t.Fatal("window closed before cleanup")
				}
			} else if err := a.AbortAndClose(); err != nil {
				t.Fatal(err)
			}
			waitSignal(t, cancelled)
			if !a.Snapshot().Closing {
				t.Fatal("missing closing state")
			}
			select {
			case <-quit:
				t.Fatal("quit before rollback settled")
			default:
			}
			// Repeated close requests must not spawn duplicate cleanup/quit calls.
			if !a.beforeClose(context.Background()) {
				t.Fatal("second close escaped cleanup")
			}
			settle <- struct{}{}
			waitSignal(t, quit)
			if a.beforeClose(context.Background()) {
				t.Fatal("finished shutdown prevented exit")
			}
			if a.store != nil || a.engine != nil {
				t.Fatal("backend resources retained")
			}
			s, err := store.Open(dir)
			if err != nil {
				t.Fatal("writer lock retained", err)
			}
			s.Close()
		})
	}
}

func TestAbortAfterCatalogOpenedClosesSafely(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := loadingFixture(t)
	if err := savePreferences(dir, "dtrpg"); err != nil {
		t.Fatal(err)
	}
	quit := make(chan struct{})
	a := &App{quit: func(context.Context) { close(quit) }}
	a.startup(context.Background())
	defer a.shutdown(context.Background())
	waitSignal(t, a.loadDone)
	if a.Snapshot().Loading {
		t.Fatal("fixture did not finish opening")
	}
	// The frontend may still be on its loading overlay because an import failed.
	if err := a.AbortAndClose(); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, quit)
	if a.beforeClose(context.Background()) {
		t.Fatal("safe shutdown did not finish")
	}
	s, err := store.InspectContext(context.Background(), dir, nil)
	if err != nil {
		t.Fatal("close did not release the catalog", err)
	}
	defer s.Close()
	if s.Version() != 1 {
		t.Fatal("closing changed the catalog schema")
	}
}

func TestAsyncCatalogOpenCompletesAndFailedSwitchKeepsPrevious(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := savePreferences(loadingFixture(t), "dtrpg"); err != nil {
		t.Fatal(err)
	}
	a := &App{}
	a.startup(context.Background())
	defer a.shutdown(context.Background())
	waitSignal(t, a.loadDone)
	if v := a.Snapshot(); v.StorageReady || !v.Maintenance.NeedsSchema {
		t.Fatal("opening upgraded a legacy catalog", v)
	}
	if err := a.StartMaintenance("schema"); err != nil {
		t.Fatal(err)
	}
	if err := a.maintenance.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v := a.Snapshot(); v.Loading || !v.StorageReady || v.LoadingError != "" {
		t.Fatal("loading did not finish", v)
	}
	old := a.store
	locked, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Close()
	a.mu.Lock()
	a.startLoadLocked(locked.Dir, true)
	done := a.loadDone
	a.mu.Unlock()
	waitSignal(t, done)
	if v := a.Snapshot(); v.Loading || !v.StorageReady || v.LoadingError == "" || a.store != old {
		t.Fatal("failed folder switch discarded working catalog", v)
	}
}
