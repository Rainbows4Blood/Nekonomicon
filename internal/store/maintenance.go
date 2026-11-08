package store

import (
	"context"
	"errors"

	"github.com/Rainbows4Blood/Nekonomicon/internal/catalog"
	"github.com/Rainbows4Blood/Nekonomicon/internal/groups"
	"github.com/Rainbows4Blood/Nekonomicon/internal/metadata"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

// MaintenanceJobs reads the two fixed storefront checkpoints. It never creates
// or updates them: opening a catalog must remain inspection-only.
func (s *Store) MaintenanceJobs(ctx context.Context, action string) ([]MetadataJob, error) {
	var jobs []MetadataJob
	for _, source := range []storefront.Source{storefront.DTRPG, storefront.DMSGuild} {
		read := s.MetadataJob
		if action == "groups" {
			read = s.GroupsJob
		}
		if action == "catalog" {
			read = s.CatalogJob
		}
		j, err := read(ctx, source)
		if err != nil {
			return nil, err
		}
		if j != nil {
			jobs = append(jobs, *j)
		}
	}
	return jobs, nil
}

// BeginDatabaseMaintenance claims both storefronts atomically. Their independent
// cursors keep overlapping product IDs distinct. Legacy paused passes retain
// their committed cursor, while the other store joins the explicit operation.
func (s *Store) BeginDatabaseMaintenance(ctx context.Context, action string) ([]MetadataJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Version() != SchemaVersion {
		return nil, errors.New("Apply the database update first")
	}
	table, version := "metadata_jobs", metadata.Version
	summary := s.MetadataSummary
	if action == "groups" {
		table, version = "group_jobs", groups.Version
		summary = s.GroupsSummary
	}
	if action == "catalog" {
		table, version, summary = "catalog_jobs", catalog.Version, s.CatalogSummary
	}
	if action != "metadata" && action != "groups" && action != "catalog" {
		return nil, errors.New("Unknown maintenance task")
	}
	var held int
	if err := s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM runs WHERE finished_at IS NULL)+(SELECT count(*) FROM (SELECT 'metadata' AS task,state FROM metadata_jobs UNION ALL SELECT 'groups',state FROM group_jobs UNION ALL SELECT 'catalog',state FROM catalog_jobs) WHERE task<>? AND state IN ('running','paused','blocked'))`, action).Scan(&held); err != nil {
		return nil, err
	}
	if held != 0 {
		return nil, errors.New("End the unfinished crawl or other maintenance task first")
	}
	old, err := s.MaintenanceJobs(ctx, action)
	if err != nil {
		return nil, err
	}
	var jobs []MetadataJob
	for _, source := range []storefront.Source{storefront.DTRPG, storefront.DMSGuild} {
		j := MetadataJob{Source: source, Version: version, State: "running"}
		resuming := false
		for _, previous := range old {
			if previous.Source == source && previous.Version == version && maintenanceHeld(previous.State) {
				j = previous
				j.State = "running"
				resuming = true
			}
		}
		if !resuming {
			v, err := summary(ctx, source)
			if err != nil {
				return nil, err
			}
			j.Total = v.Pending
		}
		jobs = append(jobs, j)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO `+table+` VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(source) DO UPDATE SET version=excluded.version,state=excluded.state,total=excluded.total,processed=excluded.processed,failed=excluded.failed,warned=excluded.warned,last_id=excluded.last_id`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	for _, j := range jobs {
		if _, err = stmt.ExecContext(ctx, j.Source, j.Version, j.State, j.Total, j.Processed, j.Failed, j.Warned, j.LastID); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return jobs, nil
}

func maintenanceHeld(state string) bool {
	return state == "running" || state == "paused" || state == "blocked"
}

// Keep both checkpoints held until the entire pass finishes, even when one
// storefront is empty or already fully processed. Resume never retries failures
// already visited in this pass, including across a storefront boundary.
func (s *Store) SetDatabaseMaintenanceState(ctx context.Context, action string, state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	table := "metadata_jobs"
	if action == "groups" {
		table = "group_jobs"
	}
	if action == "catalog" {
		table = "catalog_jobs"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE `+table+` SET state=? WHERE state IN ('running','paused','blocked')`, state)
	return err
}
