package main

import (
	"context"
	"errors"
	"time"

	"github.com/Rainbows4Blood/Nekonomicon/internal/enrichment"
	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
)

func (a *App) enrichmentRunning() bool { return a.enrichment != nil && a.enrichment.Snapshot().Running }
func (a *App) StartEnrichment() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.loading || a.closing || a.busy || a.store == nil || a.store.Version() != store.SchemaVersion {
		return errors.New("Apply the database update and open a compatible catalog first")
	}
	if a.maintenanceHeld() || a.engine != nil && (a.engine.Snapshot().Running || a.engine.Snapshot().Run != nil) {
		return errors.New("Finish the current crawl or maintenance before enriching")
	}
	if a.openAI == nil || !a.openAI.Snapshot().PlanEnabled || a.openAI.Snapshot().Running {
		return errors.New("Connect OpenAI with ChatGPT plan access in the sidebar first")
	}
	id := a.openAI.Snapshot().Active
	ai := a.openAI
	// Serialize token refresh/storage while allowing concurrent inference.
	tokenGate := make(chan struct{}, 1)
	client := enrichment.NewClient(func(ctx context.Context) (string, error) {
		select {
		case tokenGate <- struct{}{}:
			defer func() { <-tokenGate }()
		case <-ctx.Done():
			return "", ctx.Err()
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		token, err := ai.AccessToken(ctx)
		if err != nil {
			return "", err
		}
		if ai.Snapshot().Active != id {
			return "", errors.New("OpenAI account changed")
		}
		return token, nil
	})
	if a.enrichment == nil {
		a.enrichment = &enrichment.Runner{}
	}
	return a.enrichment.Start(a.ctx, a.store, client)
}
func (a *App) StopEnrichment() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.enrichment != nil {
		a.enrichment.Stop()
	}
}

type EnrichmentDashboard struct {
	Counts store.EnrichmentCounts `json:"counts"`
	Books  []store.EnrichmentBook `json:"books"`
}

func (a *App) EnrichmentDashboard(offset int) (EnrichmentDashboard, error) {
	var d EnrichmentDashboard
	s, err := a.catalogStore()
	if err != nil {
		return d, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	d.Counts, err = s.EnrichmentCounts(ctx)
	if err != nil {
		return d, errors.New("Could not read enrichment counts")
	}
	d.Books, err = s.EnrichmentHistory(ctx, offset)
	return d, err
}
