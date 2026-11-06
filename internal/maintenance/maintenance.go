// Package maintenance runs explicit local maintenance independently of Wails.
package maintenance

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Rainbows4Blood/Nekonomicon/internal/metadata"
	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

type Snapshot struct {
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
	mu     sync.Mutex
	s      *store.Store
	source storefront.Source
	v      Snapshot
	cancel context.CancelFunc
	done   chan struct{}
}

func New(ctx context.Context, s *store.Store, source storefront.Source) (*Manager, error) {
	version := s.Version()
	m := &Manager{s: s, source: source, v: Snapshot{State: "ready", Message: "Apply missing transformations to saved responses. No sign-in is needed.", SchemaVersion: version, ExtractionVersion: metadata.Version, NeedsSchema: version != store.SchemaVersion, NewerSchema: version > store.SchemaVersion}}
	if m.v.NeedsSchema {
		m.v.State = "schema_required"
		m.v.Message = "Database needs maintenance before crawling."
		if m.v.NewerSchema {
			m.v.Message = "This catalog needs a newer version of Nekonomicon."
		}
		return m, nil
	}
	v, err := s.MetadataSummary(ctx, source)
	if err != nil {
		return nil, err
	}
	m.v.Summary = v
	j, err := s.MetadataJob(ctx, source)
	if err != nil {
		return nil, err
	}
	if j != nil {
		m.v.Job = j
		m.v.State = j.State
		if j.State == "running" || j.State == "paused" || j.State == "blocked" {
			m.v.Held = true
			m.v.State = "paused"
			m.v.Message = "Maintenance can resume from its last committed batch."
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
func (m *Manager) Start(action string, afterSchema func() (storefront.Source, error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.v.Running {
		return errors.New("Maintenance is already running")
	}
	if action != "schema" && action != "metadata" {
		return errors.New("Unknown maintenance action")
	}
	if action == "metadata" && m.v.NeedsSchema {
		return errors.New("Apply the database update first")
	}
	if action == "schema" && (!m.v.NeedsSchema || m.v.NewerSchema) {
		return errors.New("No supported database update is available")
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.done = make(chan struct{})
	m.v.Running = true
	m.v.State = "starting"
	m.v.Message = "Preparing maintenance…"
	go m.run(ctx, action, afterSchema)
	return nil
}
func (m *Manager) run(ctx context.Context, action string, afterSchema func() (storefront.Source, error)) {
	var err error
	started := false
	defer func() {
		state, msg := "completed", "Maintenance complete. Saved responses are unchanged."
		if err != nil {
			state, msg = "blocked", "Maintenance could not finish. Check local storage and retry."
			if errors.Is(err, context.Canceled) {
				state, msg = "paused", "Paused. Committed work is retained."
			}
		}
		if action == "metadata" && started {
			v := m.Snapshot()
			if err == nil && v.Job != nil && (v.Job.Failed > 0 || v.Job.Warned > 0) {
				state, msg = "completed_warnings", "Finished with issues. Review warnings and retry failed extractions below."
			}
			finishCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			if saveErr := m.s.SetMetadataState(finishCtx, m.source, state); saveErr != nil {
				state, msg = "blocked", "Could not save maintenance status. Committed batches are safe; resume to continue."
			}
			cancel()
		}
		if m.s.Version() == store.SchemaVersion {
			c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			if summary, e := m.s.MetadataSummary(c, m.source); e == nil {
				m.change(func(v *Snapshot) { v.Summary = summary })
			} else if err == nil {
				state, msg = "blocked", "Could not refresh maintenance counts. Reopen the catalog to check the saved results."
			}
			cancel()
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		m.v.State, m.v.Message, m.v.Running = state, msg, false
		m.v.SchemaVersion = m.s.Version()
		m.v.NeedsSchema = m.v.SchemaVersion != store.SchemaVersion
		m.v.Held = started && (state == "paused" || state == "blocked")
		if m.v.Job != nil {
			m.v.Job.State = state
		}
		m.cancel()
		close(m.done)
	}()
	if action == "schema" {
		err = m.s.Upgrade(ctx, func(msg string) { m.change(func(v *Snapshot) { v.State = "upgrading"; v.Message = msg }) })
		if err == nil && afterSchema != nil {
			var source storefront.Source
			source, err = afterSchema()
			if err == nil {
				m.mu.Lock()
				m.source = source
				m.mu.Unlock()
			}
		}
		return
	}
	var job store.MetadataJob
	job, err = m.s.BeginMetadata(ctx, m.source)
	if err != nil {
		return
	}
	started = true
	m.change(func(v *Snapshot) {
		j := job
		v.Job = &j
		v.Held = true
		v.State = "extracting"
		v.Message = "Extracting metadata from saved responses…"
	})
	for {
		if err = ctx.Err(); err != nil {
			return
		}
		var rows []store.MetadataRow
		rows, err = m.s.MetadataPage(ctx, m.source, job.LastID)
		if err != nil || len(rows) == 0 {
			return
		}
		if err = ctx.Err(); err != nil {
			return
		}
		// Pause lets the current short transaction settle. No buffered result is
		// counted until its rows, outcome and cursor have committed together.
		commitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		job, err = m.s.CommitMetadata(commitCtx, job, rows)
		cancel()
		if err != nil {
			return
		}
		m.change(func(v *Snapshot) { j := job; v.Job = &j })
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
		if err := m.s.SetMetadataState(context.Background(), m.source, "ended"); err != nil {
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
	v, err := m.s.MetadataSummary(ctx, m.source)
	if err != nil {
		return err
	}
	m.change(func(s *Snapshot) { s.Summary = v })
	return nil
}
