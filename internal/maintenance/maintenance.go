// Package maintenance runs explicit local maintenance independently of Wails.
package maintenance

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Rainbows4Blood/Nekonomicon/internal/metadata"
	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
)

type Snapshot struct {
	Action  string               `json:"action"`
	Groups  store.GroupsSummary  `json:"groups"`
	Catalog store.CatalogSummary `json:"catalog"`

	CountsStale       bool                  `json:"countsStale"`
	State             string                `json:"state"`
	Message           string                `json:"message"`
	Running           bool                  `json:"running"`
	Held              bool                  `json:"held"`
	NeedsSchema       bool                  `json:"needsSchema"`
	NewerSchema       bool                  `json:"newerSchema"`
	SchemaVersion     int                   `json:"schemaVersion"`
	ExtractionVersion int                   `json:"extractionVersion"`
	Summary           store.MetadataSummary `json:"summary"`
	Job               *store.MetadataJob    `json:"job"`
}
type Manager struct {
	mu          sync.Mutex
	s           *store.Store
	v           Snapshot
	cancel      context.CancelFunc
	done        chan struct{}
	readSummary func(context.Context, string) (store.MetadataSummary, error)
}

func New(ctx context.Context, s *store.Store) (*Manager, error) {
	version := s.Version()
	m := &Manager{s: s, v: Snapshot{State: "ready", Message: "Apply missing transformations to saved responses. No sign-in is needed.", SchemaVersion: version, ExtractionVersion: metadata.Version, NeedsSchema: version != store.SchemaVersion, NewerSchema: version > store.SchemaVersion}}
	m.readSummary = func(ctx context.Context, action string) (store.MetadataSummary, error) {
		switch action {
		case "groups":
			return s.GroupsSummary(ctx, "")
		case "catalog":
			return s.CatalogSummary(ctx, "")
		default:
			return s.MetadataSummary(ctx, "")
		}
	}
	if m.v.NeedsSchema {
		m.v.State = "schema_required"
		m.v.Message = "Database needs maintenance before crawling."
		if m.v.NewerSchema {
			m.v.Message = "This catalog needs a newer version of Nekonomicon."
		}
		return m, nil
	}
	v, err := s.MetadataSummary(ctx, "")
	if err != nil {
		return nil, err
	}
	m.v.Summary = v
	if m.v.Groups, err = s.GroupsSummary(ctx, ""); err != nil {
		return nil, err
	}
	if m.v.Catalog, err = s.CatalogSummary(ctx, ""); err != nil {
		return nil, err
	}
	for _, action := range []string{"metadata", "groups", "catalog"} {
		jobs, err := s.MaintenanceJobs(ctx, action)
		if err != nil {
			return nil, err
		}
		var held []store.MetadataJob
		for _, j := range jobs {
			if j.State == "running" || j.State == "paused" || j.State == "blocked" {
				held = append(held, j)
			}
		}
		if len(held) > 0 {
			m.v.Action, m.v.Held, m.v.State = action, true, "paused"
			m.v.Job = combinedJob(held)
			m.v.Message = "Maintenance can resume across the whole database from its last committed batch."
			break
		}
		if len(jobs) > 0 && m.v.Job == nil {
			m.v.Action, m.v.Job = action, combinedJob(jobs)
			m.v.State = m.v.Job.State
		}
	}
	return m, nil
}
func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := m.v
	if v.Job != nil {
		j := *v.Job
		v.Job = &j
	}
	return v
}
func (m *Manager) change(f func(*Snapshot)) { m.mu.Lock(); defer m.mu.Unlock(); f(&m.v) }
func (m *Manager) Start(action string, afterSchema func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.v.Running {
		return errors.New("Maintenance is already running")
	}
	if action != "schema" && action != "metadata" && action != "groups" && action != "catalog" {
		return errors.New("Unknown maintenance action")
	}
	if action != "schema" && m.v.NeedsSchema {
		return errors.New("Apply the database update first")
	}
	if action == "schema" && (!m.v.NeedsSchema || m.v.NewerSchema) {
		return errors.New("No supported database update is available")
	}
	if m.v.Held && action != m.v.Action {
		return errors.New("Resume or end the current maintenance task first")
	}
	m.v.Action = action
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.done = make(chan struct{})
	m.v.Running = true
	m.v.State = "starting"
	m.v.Message = "Preparing maintenance…"
	go m.run(ctx, action, afterSchema)
	return nil
}
func (m *Manager) run(ctx context.Context, action string, afterSchema func() error) {
	var err error
	started := false
	previouslyHeld := m.Snapshot().Held
	var restoredAction string
	var restoredJob *store.MetadataJob
	defer func() {
		state, msg := "completed", "Maintenance complete. Saved responses are unchanged."
		if err != nil {
			state, msg = "blocked", "Maintenance could not finish. Check local storage and retry."
			if errors.Is(err, context.Canceled) {
				state, msg = "paused", "Paused. Committed work is retained."
			}
		}
		if action != "schema" && started {
			v := m.Snapshot()
			if err == nil && v.Job != nil && (v.Job.Failed > 0 || v.Job.Warned > 0) {
				state, msg = "completed_warnings", "Finished with issues. Review the task’s warnings and retry failures below."
			}
			finishCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			if saveErr := m.setState(finishCtx, action, state); saveErr != nil {
				state, msg = "blocked", "Could not save maintenance status. Committed batches are safe; resume to continue."
			}
			cancel()
		}
		if m.s.Version() == store.SchemaVersion {
			// Counts are a read-only follow-up, never a reason to undo a saved
			// completion or hold the catalog for a needless second indexing pass.
			_ = m.refreshCounts(context.Background())
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		m.v.State, m.v.Message, m.v.Running = state, msg, false
		m.v.SchemaVersion = m.s.Version()
		m.v.NeedsSchema = m.v.SchemaVersion != store.SchemaVersion
		m.v.Held = (started || previouslyHeld) && (state == "paused" || state == "blocked")
		if m.v.Job != nil {
			m.v.Job.State = state
		}
		if restoredJob != nil {
			m.v.Action, m.v.Job, m.v.Held = restoredAction, restoredJob, true
			m.v.State = "paused"
			m.v.Message = "Database updated. Resume or end the unfinished maintenance task."
		}
		m.cancel()
		close(m.done)
	}()
	if action == "schema" {
		err = m.s.Upgrade(ctx, func(msg string) { m.change(func(v *Snapshot) { v.State = "upgrading"; v.Message = msg }) })
		if err == nil && afterSchema != nil {
			err = afterSchema()
		}
		if err == nil {
			// A schema upgrade must not hide a paused task from the older schema.
			readCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			for _, task := range []string{"metadata", "groups", "catalog"} {
				var jobs []store.MetadataJob
				jobs, err = m.s.MaintenanceJobs(readCtx, task)
				if err != nil {
					break
				}
				var held []store.MetadataJob
				for _, j := range jobs {
					if j.State == "running" || j.State == "paused" || j.State == "blocked" {
						held = append(held, j)
					}
				}
				if len(held) > 0 {
					restoredAction, restoredJob = task, combinedJob(held)
					break
				}
			}
		}
		return
	}
	jobs, beginErr := m.s.BeginDatabaseMaintenance(ctx, action)
	err = beginErr
	if err != nil {
		return
	}
	started = true
	m.change(func(v *Snapshot) {
		v.Job = combinedJob(jobs)
		v.Held = true
		v.State = "extracting"
		v.Message = "Extracting metadata from saved responses…"
		if action == "groups" {
			v.State = "grouping"
			v.Message = "Building D&D edition groups from saved responses…"
		}
		if action == "catalog" {
			v.State = "indexing"
			v.Message = "Indexing names, descriptions, editions and release dates…"
		}
	})
	for i, job := range jobs {
		for {
			if err = ctx.Err(); err != nil {
				return
			}
			var rows []store.MetadataRow
			if action == "groups" {
				rows, err = m.s.GroupsPage(ctx, job.Source, job.LastID)
			} else if action == "catalog" {
				rows, err = m.s.CatalogPage(ctx, job.Source, job.LastID)
			} else {
				rows, err = m.s.MetadataPage(ctx, job.Source, job.LastID)
			}
			if err != nil {
				return
			}
			if len(rows) == 0 {
				break
			}
			if err = ctx.Err(); err != nil {
				return
			}
			// Pause lets the current short transaction settle. No buffered result is
			// counted until its rows, outcome and cursor have committed together.
			commitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if action == "groups" {
				job, err = m.s.CommitGroups(commitCtx, job, rows)
			} else if action == "catalog" {
				job, err = m.s.CommitCatalog(commitCtx, job, rows)
			} else {
				job, err = m.s.CommitMetadata(commitCtx, job, rows)
			}
			cancel()
			if err != nil {
				return
			}
			jobs[i] = job
			m.change(func(v *Snapshot) { v.Job = combinedJob(jobs) })
		}
	}
}
func (m *Manager) Pause() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.v.Running {
		return errors.New("No maintenance is running")
	}
	m.v.State = "pausing"
	m.v.Message = "Waiting for the current batch to finish…"
	m.cancel()
	return nil
}
func (m *Manager) End() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.v.Running {
		return errors.New("Pause maintenance before ending it")
	}
	if m.v.Held {
		if err := m.setState(context.Background(), m.v.Action, "ended"); err != nil {
			return err
		}
	}
	m.v.Held = false
	m.v.State = "ended"
	m.v.Message = "Maintenance ended. Completed work is retained."
	return nil
}
func (m *Manager) Shutdown() {
	m.mu.Lock()
	if !m.v.Running {
		m.mu.Unlock()
		return
	}
	done := m.done
	m.cancel()
	m.mu.Unlock()
	<-done
}
func (m *Manager) Wait(ctx context.Context) error {
	m.mu.Lock()
	done, running := m.done, m.v.Running
	m.mu.Unlock()
	if !running {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *Manager) Refresh(ctx context.Context) error {
	m.mu.Lock()
	if m.v.Running || m.v.NeedsSchema {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	return m.refreshCounts(ctx)
}

func (m *Manager) refreshCounts(ctx context.Context) error {
	var failure error
	for _, action := range []string{"metadata", "groups", "catalog"} {
		// Each count has its own budget; a slow first query cannot exhaust the
		// remaining queries' time. The caller can still cancel the whole refresh.
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		v, err := m.readSummary(readCtx, action)
		cancel()
		if err != nil {
			failure = err
			continue
		}
		m.change(func(s *Snapshot) {
			switch action {
			case "groups":
				s.Groups = v
			case "catalog":
				s.Catalog = v
			default:
				s.Summary = v
			}
		})
	}
	m.change(func(s *Snapshot) { s.CountsStale = failure != nil })
	if failure != nil {
		return errors.New("Some counts could not be refreshed. Saved work is safe; use Refresh counts to retry")
	}
	return nil
}

func (m *Manager) setState(ctx context.Context, action, state string) error {
	return m.s.SetDatabaseMaintenanceState(ctx, action, state)
}

func combinedJob(jobs []store.MetadataJob) *store.MetadataJob {
	v := &store.MetadataJob{}
	for _, j := range jobs {
		v.Version, v.State = j.Version, j.State
		v.Total += j.Total
		v.Processed += j.Processed
		v.Warned += j.Warned
		v.Failed += j.Failed
	}
	return v
}
