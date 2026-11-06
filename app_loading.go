package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/Rainbows4Blood/Nekonomicon/internal/engine"
	"github.com/Rainbows4Blood/Nekonomicon/internal/maintenance"
	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) startup(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ctx, a.cancel = context.WithCancel(ctx)
	a.source, a.folder = storefront.DTRPG, defaultDataDir()
	var settings preferences
	if b, err := os.ReadFile(filepath.Join(configDir(), "settings.json")); err == nil && len(b) < 8192 && json.Unmarshal(b, &settings) == nil && filepath.IsAbs(settings.DataFolder) {
		a.folder = settings.DataFolder
		if settings.Source.Valid() {
			a.source = settings.Source
		}
	}
	a.startLoadLocked(a.folder, false)
}

// Start events only once the Wails frontend is available. Snapshot also exposes
// the current state when the frontend subscribes after the first load event.
func (a *App) domReady(context.Context) {
	a.eventsOnce.Do(func() { go a.emitProgress() })
}

func (a *App) emitProgress() {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			wruntime.EventsEmit(a.ctx, "progress", a.Snapshot())
		}
	}
}

// Caller holds mu; only this worker may replace the catalog until it completes.
func (a *App) startLoadLocked(folder string, persist bool) {
	ctx, cancel := context.WithCancel(a.ctx)
	done := make(chan struct{})
	a.loadCancel, a.loadDone = cancel, done
	a.loading, a.busy, a.migrating = true, true, false
	a.loadingMessage = "Opening your local catalog…"
	a.storageError = ""
	go func() {
		defer cancel()
		err := a.openCatalog(ctx, folder, persist)
		a.mu.Lock()
		defer a.mu.Unlock()
		if err != nil && !a.closing {
			a.storageError = "Local storage is unavailable or already in use. Choose another data folder or close the other instance."
			if errors.Is(err, store.ErrUpgrade) {
				a.storageError = err.Error()
			}
		}
		a.loading, a.migrating, a.busy = false, false, a.closing
		a.loadCancel = nil
		close(done)
	}()
}

func (a *App) openCatalog(ctx context.Context, folder string, persist bool) error {
	a.mu.Lock()
	source, n, vault, note := a.source, a.network, a.vault, a.authNote
	ready := a.engine != nil && a.engine.Snapshot().ConnectionReady
	open := a.openStore
	a.mu.Unlock()
	if open == nil {
		open = store.InspectContext
	}
	s, err := open(ctx, folder, func(message string) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if !a.closing {
			a.migrating = true
			a.loadingMessage = message
		}
	})
	if err != nil {
		return err
	}
	installed := false
	defer func() {
		if !installed {
			s.Close()
		}
	}()
	a.mu.Lock()
	if !a.closing {
		a.loadingMessage = "Reading your saved catalog…"
	}
	a.mu.Unlock()
	if s.Version() == 1 {
		source, ready = storefront.DTRPG, false
	}
	if s.Version() >= 2 && s.Version() <= store.SchemaVersion {
		run, err := s.ActiveContext(ctx)
		if err != nil {
			return err
		}
		if run != nil && source != run.Source {
			source, ready = run.Source, false
		}
		if s.Version() == store.SchemaVersion {
			maintenanceSource, err := s.MaintenanceSource(ctx)
			if err != nil {
				return err
			}
			if maintenanceSource != "" && maintenanceSource != source {
				source, ready = maintenanceSource, false
			}
		}
	}
	if n == nil || n.Source() != source {
		n, vault, note = session(source)
	}
	var e *engine.Engine
	if s.Version() == store.SchemaVersion {
		e, err = engine.NewContext(ctx, s, n, engine.DefaultConfig())
		if err != nil {
			return err
		}
		_ = e.SetReady(ready)
	}
	m, err := maintenance.New(ctx, s, source)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if persist {
		if err = savePreferences(s.Dir, source); err != nil {
			return err
		}
	}
	a.mu.Lock()
	if a.closing || ctx.Err() != nil {
		a.mu.Unlock()
		return context.Canceled
	}
	old := a.store
	a.source, a.network, a.vault, a.authNote = source, n, vault, note
	a.store, a.engine, a.folder, a.storageError = s, e, s.Dir, ""
	a.maintenance = m
	installed = true
	a.mu.Unlock()
	// Keep loading/close waits active until the previous connection is closed.
	if old != nil {
		old.Close()
	}
	return nil
}

func (a *App) ChooseDataFolder() error {
	a.mu.Lock()
	if a.busy || a.closing || a.maintenanceHeld() || a.engine != nil && (a.engine.Snapshot().Running || a.engine.Snapshot().Run != nil) {
		a.mu.Unlock()
		return errors.New("End the current run before changing the data folder")
	}
	a.busy = true
	a.mu.Unlock()
	folder, err := wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{Title: "Choose Nekonomicon data folder"})
	a.mu.Lock()
	defer a.mu.Unlock()
	a.busy = a.closing
	if a.closing {
		return context.Canceled
	}
	if err != nil {
		return errors.New("Could not open the folder chooser")
	}
	if folder == "" || filepath.Clean(folder) == filepath.Clean(a.folder) && a.engine != nil {
		return nil
	}
	a.startLoadLocked(folder, true)
	return nil
}

// Window close and Abort both wait for rollback/commit settlement, connection
// closure and writer-lock release before asking Wails to exit this process.
func (a *App) beforeClose(context.Context) bool {
	a.mu.Lock()
	complete := a.shutdownComplete
	a.mu.Unlock()
	if complete {
		return false
	}
	a.requestClose()
	return true
}

func (a *App) AbortAndClose() error {
	a.mu.Lock()
	allowed := a.loading || a.closing
	a.mu.Unlock()
	if !allowed {
		return errors.New("Catalog loading has finished")
	}
	a.requestClose()
	return nil
}

func (a *App) requestClose() {
	a.mu.Lock()
	if a.closing {
		a.mu.Unlock()
		return
	}
	a.closing, a.busy = true, true
	a.loadingMessage = "Waiting for database work to stop safely…"
	ctx, quit := a.ctx, a.quit
	if quit == nil {
		quit = wruntime.Quit
	}
	a.mu.Unlock()
	go func() { a.shutdown(ctx); quit(ctx) }()
}

func (a *App) shutdown(context.Context) {
	a.shutdownOnce.Do(func() {
		a.mu.Lock()
		a.closing, a.busy = true, true
		loadCancel, done := a.loadCancel, a.loadDone
		a.mu.Unlock()
		if loadCancel != nil {
			loadCancel()
		}
		if done != nil {
			<-done
		}
		a.mu.Lock()
		e, s, cancel, m := a.engine, a.store, a.cancel, a.maintenance
		a.mu.Unlock()
		if m != nil {
			m.Shutdown()
		}
		if e != nil {
			e.Shutdown()
		}
		if s != nil {
			s.Close()
		}
		if cancel != nil {
			cancel()
		}
		a.mu.Lock()
		a.engine, a.store = nil, nil
		a.shutdownComplete = true
		a.mu.Unlock()
	})
}
