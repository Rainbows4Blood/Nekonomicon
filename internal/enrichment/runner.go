package enrichment

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Rainbows4Blood/Nekonomicon/internal/enrichdata"
	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

const workerCount = 16

type Provider interface {
	Check(context.Context) error
	Extract(context.Context, store.EnrichmentBook, string, bool, func(string)) (enrichdata.Result, error)
}
type ActiveBook struct {
	Source  storefront.Source `json:"source"`
	ID      int64             `json:"id"`
	Name    string            `json:"name"`
	Stage   string            `json:"stage"`
	Message string            `json:"message"`
}
type Snapshot struct {
	Running   bool         `json:"running"`
	State     string       `json:"state"`
	Message   string       `json:"message"`
	Active    []ActiveBook `json:"active"`
	Processed int64        `json:"processed"`
	Total     int64        `json:"total"`
	Enriched  int64        `json:"enriched"`
	Unknown   int64        `json:"unknown"`
	Failed    int64        `json:"failed"`
	WebPasses int64        `json:"webPasses"`
}
type Runner struct {
	mu      sync.Mutex
	v       Snapshot
	cancel  context.CancelFunc
	done    chan struct{}
	newGate func() *retryGate // nil uses the production backoff; injectable for local tests.
}

func (r *Runner) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	v := r.v
	v.Active = append([]ActiveBook{}, v.Active...)
	return v
}
func (r *Runner) change(f func(*Snapshot)) { r.mu.Lock(); defer r.mu.Unlock(); f(&r.v) }
func (r *Runner) Start(parent context.Context, s *store.Store, p Provider) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.v.Running {
		return errors.New("Enrichment is already running")
	}
	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel
	r.done = make(chan struct{})
	r.v = Snapshot{Running: true, State: "preparing", Message: "Checking eligible adventures and OpenAI access…"}
	go r.run(ctx, s, p)
	return nil
}
func (r *Runner) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.v.Running {
		r.v.State = "stopping"
		r.v.Message = "Stopping. Saved results are retained."
		r.cancel()
	}
}
func (r *Runner) Shutdown() {
	r.Stop()
	r.mu.Lock()
	done := r.done
	r.mu.Unlock()
	if done != nil {
		<-done
	}
}

// Workers acknowledge every checkpoint through the coordinator. Only that
// coordinator writes, and a book cannot begin web fallback before its first
// pass is committed. The unbuffered channels bound retained input/output data.
type update struct {
	book     store.EnrichmentBook
	result   enrichdata.Result
	hash     string
	err      error
	terminal bool
	ack      chan error
}

func (r *Runner) run(ctx context.Context, s *store.Store, p Provider) {
	var failure error
	defer func() {
		r.change(func(v *Snapshot) {
			v.Running = false
			v.Active = nil
			v.State = "completed"
			v.Message = "Pass complete. Unknown values count as enriched; unfinished books can be retried with Enrich."
			if failure != nil {
				v.State, v.Message = "blocked", failure.Error()
			}
			if ctx.Err() != nil {
				v.State, v.Message = "stopped", "Stopped. Enrich continues with unfinished enrichments."
			}
			r.cancel()
			close(r.done)
		})
	}()
	counts, err := s.EnrichmentCounts(ctx)
	if err != nil {
		failure = errors.New("Could not read enrichment counts")
		return
	}
	r.change(func(v *Snapshot) { v.Total = counts.Pending })
	if counts.Pending == 0 {
		return
	}
	gate := newRetryGate()
	if r.newGate != nil {
		gate = r.newGate()
	}
	if err = gate.call(ctx, func(until time.Time) {
		r.change(func(v *Snapshot) {
			if v.State != "stopping" {
				v.State, v.Message = "waiting", retryMessage(until)
			}
		})
	}, func(callCtx context.Context) error {
		r.change(func(v *Snapshot) {
			if v.State != "stopping" {
				v.State, v.Message = "preparing", "Checking eligible adventures and OpenAI access…"
			}
		})
		return p.Check(callCtx)
	}); err != nil {
		failure = err
		return
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	r.change(func(v *Snapshot) {
		if v.State != "stopping" {
			v.State, v.Message = "running", "Enriching up to 16 books at a time. Fast mode requested…"
		}
	})
	jobs := make(chan store.EnrichmentBook)
	updates := make(chan update)
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		defer close(jobs)
		var source storefront.Source
		var id int64
		for workCtx.Err() == nil {
			b, e := s.NextEnrichment(workCtx, source, id)
			if e != nil {
				select {
				case updates <- update{err: errors.New("Could not read the next adventure")}:
				case <-workCtx.Done():
				}
				return
			}
			if b == nil {
				return
			}
			source, id = b.Source, b.ID
			select {
			case jobs <- *b:
			case <-workCtx.Done():
				return
			}
		}
	}()
	for i := 0; i < workerCount; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for b := range jobs {
				if workCtx.Err() != nil {
					return
				}
				r.process(workCtx, s, p, gate, b, updates)
			}
		}()
	}
	go func() { group.Wait(); close(updates) }()
	storageFailed := false
	for u := range updates {
		if u.ack == nil {
			if failure == nil {
				failure = u.err
			}
			cancel()
			continue
		}
		var stop StopError
		if errors.As(u.err, &stop) {
			if failure == nil {
				failure = u.err
			}
			cancel()
		}
		message := ""
		if u.err != nil {
			message = u.err.Error()
		}
		e := failure
		if !storageFailed {
			e = save(s, u.book, u.result, u.hash, message)
		}
		if e != nil {
			storageFailed = true
			failure = errors.New("Could not save enrichment. Committed results are safe; check local storage before retrying")
			cancel()
		} else if u.terminal {
			r.change(func(v *Snapshot) {
				v.Processed++
				if u.err != nil {
					v.Failed++
				} else {
					v.Enriched++
					if !u.result.Complete() {
						v.Unknown++
					}
				}
			})
		}
		u.ack <- e
	}
}

func (r *Runner) stage(b store.EnrichmentBook, stage, message string) {
	r.change(func(v *Snapshot) {
		for i := range v.Active {
			if v.Active[i].Source == b.Source && v.Active[i].ID == b.ID {
				v.Active[i].Stage, v.Active[i].Message = stage, message
				return
			}
		}
		v.Active = append(v.Active, ActiveBook{Source: b.Source, ID: b.ID, Name: b.Name, Stage: stage, Message: message})
	})
}
func (r *Runner) process(ctx context.Context, s *store.Store, p Provider, gate *retryGate, b store.EnrichmentBook, updates chan<- update) {
	r.stage(b, "storefront", "Reading the cover and full storefront JSON…")
	defer r.change(func(v *Snapshot) {
		for i := range v.Active {
			if v.Active[i].Source == b.Source && v.Active[i].ID == b.ID {
				v.Active = append(v.Active[:i], v.Active[i+1:]...)
				break
			}
		}
	})
	cover, hash, workErr := coverImage(ctx, s, b)
	if len(b.Raw) > 16<<20 {
		workErr = errors.New("Saved JSON exceeds the supported input size; it was not truncated or sent")
	}
	result := b.Existing
	persist := func(terminal bool, err error) error {
		ack := make(chan error, 1)
		updates <- update{book: b, result: result, hash: hash, err: err, terminal: terminal, ack: ack}
		return <-ack
	}
	for _, web := range []bool{false, true} {
		if workErr != nil || ctx.Err() != nil {
			break
		}
		stage := "storefront"
		message := "Reading the cover and full storefront JSON…"
		if cover == "" {
			message = "Reading the full storefront JSON without a cover…"
		}
		if web {
			stage = "web"
			message = "Checking missing information with web search available…"
			r.change(func(v *Snapshot) { v.WebPasses++ })
		}
		b.Existing = result
		var next enrichdata.Result
		err := gate.call(ctx, func(until time.Time) { r.stage(b, "waiting", retryMessage(until)) }, func(callCtx context.Context) error {
			r.stage(b, stage, message)
			var e error
			next, e = p.Extract(callCtx, b, cover, web, func(msg string) { r.stage(b, stage, msg) })
			return e
		})
		if err != nil {
			workErr = err
			break
		}
		merged := result
		if !b.PlayersDone {
			merged.Players = enrichdata.Merge(result.Players, next.Players)
		}
		if !b.LevelsDone {
			merged.Levels = enrichdata.Merge(result.Levels, next.Levels)
		}
		if !b.SettingsDone && !result.Settings.Known() {
			merged.Settings = next.Settings
		}
		if !b.LocationsDone && !result.Locations.Known() {
			merged.Locations = next.Locations
		}
		if !b.DatesDone && !result.Dates.Known() {
			merged.Dates = next.Dates
		}
		if err = merged.Validate(); err != nil {
			workErr = err
			break
		}
		result = merged
		// A successful fallback finishes its requested enrichments even when
		// some or all values remain unknown. Errors never set these markers.
		b.PlayersDone = b.PlayersDone || web || result.Players.Complete()
		b.LevelsDone = b.LevelsDone || web || result.Levels.Complete()
		b.SettingsDone = b.SettingsDone || web || result.Settings.Known()
		b.LocationsDone = b.LocationsDone || web || result.Locations.Known()
		b.DatesDone = b.DatesDone || web || result.Dates.Known()
		done := b.Done()
		if persist(done, nil) != nil || done {
			return
		}
	}
	if workErr != nil && ctx.Err() == nil {
		_ = persist(true, workErr)
	}
}
func save(s *store.Store, b store.EnrichmentBook, result enrichdata.Result, hash, message string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return s.SaveEnrichment(ctx, b, result, hash, message)
}
