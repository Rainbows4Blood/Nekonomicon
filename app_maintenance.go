package main

import (
	"context"
	"errors"
	"time"

	"github.com/Rainbows4Blood/Nekonomicon/internal/engine"
	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

// Caller holds the app mutex. Paused maintenance owns the catalog until ended.
func (a *App) maintenanceHeld() bool {
	if a.maintenance == nil {
		return false
	}
	v := a.maintenance.Snapshot()
	return v.Running || v.Held
}
func (a *App) StartMaintenance(action string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.busy || a.closing || a.loading || a.maintenance == nil {
		return errors.New("Local storage is not available for maintenance")
	}
	if a.engine != nil && (a.engine.Snapshot().Running || a.engine.Snapshot().Run != nil) {
		return errors.New("End the unfinished crawl before starting maintenance")
	}
	return a.maintenance.Start(action, func() (storefront.Source, error) {
		// Called after an explicit schema update has committed, outside UI locks.
		a.mu.Lock()
		s, n, vault, note := a.store, a.network, a.vault, a.authNote
		a.mu.Unlock()
		run, err := s.Active()
		if err != nil {
			return "", err
		}
		if run != nil && run.Source != n.Source() {
			n, vault, note = session(run.Source)
		}
		e, err := engine.New(s, n, engine.DefaultConfig())
		if err != nil {
			return "", err
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.closing {
			return n.Source(), nil
		}
		if a.source != n.Source() {
			a.source = n.Source()
			a.network, a.vault, a.authNote = n, vault, note
		}
		a.engine = e
		return a.source, nil
	})
}
func (a *App) PauseMaintenance() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.maintenance == nil {
		return errors.New("No maintenance is available")
	}
	return a.maintenance.Pause()
}
func (a *App) EndMaintenance() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.maintenance == nil {
		return errors.New("No maintenance is available")
	}
	return a.maintenance.End()
}
func (a *App) RefreshMaintenance() error {
	a.mu.Lock()
	if a.busy || a.closing || a.loading || a.maintenance == nil {
		a.mu.Unlock()
		return errors.New("Wait for local storage to finish")
	}
	m := a.maintenance
	a.busy = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.busy = a.closing; a.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return m.Refresh(ctx)
}
func (a *App) MaintenanceIssues(offset int) ([]store.MetadataIssue, error) {
	a.mu.Lock()
	if a.loading || a.closing || a.store == nil || a.store.Version() != store.SchemaVersion {
		a.mu.Unlock()
		return []store.MetadataIssue{}, nil
	}
	s, source := a.store, a.source
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.MetadataIssues(ctx, source, offset)
}
