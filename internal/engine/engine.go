// Package engine runs independently of Wails. One coordinator owns the run and
// batch buffer; bounded workers only return results, never write SQLite.
package engine

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Rainbows4Blood/Nekonomicon/internal/dtrpg"
	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
)

type Config struct {
	Workers, CoverWorkers, BatchCount, BatchBytes int
	FlushInterval                                 time.Duration
}

func DefaultConfig() Config { return Config{8, 4, 1000, 32 << 20, 2 * time.Second} }

type Snapshot struct {
	SitemapFallback   bool       `json:"sitemapFallback"`
	SitemapsSkipped   int        `json:"sitemapsSkipped"`
	State             string     `json:"state"`
	Phase             string     `json:"phase"`
	Message           string     `json:"message"`
	Run               *store.Run `json:"run"`
	ConnectionReady   bool       `json:"connectionReady"`
	Running           bool       `json:"running"`
	Catalogued        int        `json:"catalogued"`
	CataloguedCovers  int        `json:"cataloguedCovers"`
	Queued            int        `json:"queued"`
	Saved             int        `json:"saved"`
	Eligible          int        `json:"eligible"`
	ProductErrors     int        `json:"productErrors"`
	MetadataWarnings  int        `json:"metadataWarnings"`
	MetadataFailures  int        `json:"metadataFailures"`
	CoverErrors       int        `json:"coverErrors"`
	Found             int        `json:"found"`
	Sitemaps          int        `json:"sitemaps"`
	CoverTotal        int        `json:"coverTotal"`
	CoverSaved        int        `json:"coverSaved"`
	CoverScanned      int        `json:"coverScanned"`
	CoverScanDone     bool       `json:"coverScanDone"`
	ProductsPerSecond float64    `json:"productsPerSecond"`
	CoversPerSecond   float64    `json:"coversPerSecond"`
	HTTPSeconds       float64    `json:"httpSeconds"`
	DecodeSeconds     float64    `json:"decodeSeconds"`
	CommitSeconds     float64    `json:"commitSeconds"`
}
type Engine struct {
	mu                                 sync.Mutex
	store                              *store.Store
	net                                *dtrpg.Network
	config                             Config
	snapshot                           Snapshot
	cancelStarts, cancelHard           context.CancelFunc
	done                               chan struct{}
	end                                bool
	pending                            []store.Result
	discovery                          *dtrpg.DiscoveryState
	committedProducts, committedCovers int
}

func New(s *store.Store, n *dtrpg.Network, c Config) (*Engine, error) {
	return NewContext(context.Background(), s, n, c)
}
func NewContext(ctx context.Context, s *store.Store, n *dtrpg.Network, c Config) (*Engine, error) {
	if c.Workers < 1 || c.Workers > 32 || c.CoverWorkers < 1 || c.CoverWorkers > 16 || c.BatchCount < 1 || c.BatchCount > 1000 || c.BatchBytes < dtrpg.ProductLimit || c.BatchBytes > 32<<20 || c.FlushInterval <= 0 || c.FlushInterval > 2*time.Second {
		return nil, errors.New("Invalid engine limits")
	}
	stats, e := s.StatsContext(ctx, n.Source())
	if e != nil {
		return nil, e
	}
	run, e := s.ActiveContext(ctx)
	if e != nil {
		return nil, e
	}
	if run != nil && run.Source != n.Source() {
		return nil, errors.New("Resume or end the other storefront run first")
	}
	v := Snapshot{State: "setup", Message: "Import cookies and test the connection to start.", Catalogued: stats.Products, CataloguedCovers: stats.Covers, Run: run}
	if run != nil {
		v.State = "paused"
		v.Phase = run.Phase
		v.Message = "An unfinished run is available. Resume it or end it to keep the committed data."
		if run.Phase == "covers" {
			v.Message = "Resume to check saved covers and download missing or changed images."
		}
		v.Saved, v.ProductErrors, e = s.RunCountsContext(ctx, run.ID)
		if e != nil {
			return nil, e
		}
		v.MetadataWarnings, v.MetadataFailures, e = s.RunMetadataCounts(ctx, run.ID)
		if e != nil {
			return nil, e
		}
	}
	return &Engine{store: s, net: n, config: c, snapshot: v}, nil
}
func (e *Engine) Snapshot() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	v := e.snapshot
	if v.Run != nil {
		copy := *v.Run
		v.Run = &copy
	}
	return v
}
func (e *Engine) change(fn func(*Snapshot)) { e.mu.Lock(); defer e.mu.Unlock(); fn(&e.snapshot) }
func (e *Engine) SetReady(ready bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.snapshot.Running {
		return errors.New("Pause the run before changing the connection")
	}
	e.snapshot.ConnectionReady = ready
	if ready && e.snapshot.State == "blocked_auth" {
		e.snapshot.State = "paused"
		e.snapshot.Message = "Connection restored. Resume the unfinished run."
	}
	if e.snapshot.Run == nil {
		if ready {
			e.snapshot.State = "ready"
			e.snapshot.Message = "Connection checked. Ready to discover products."
		} else {
			e.snapshot.State = "setup"
			e.snapshot.Message = "Test the connection before starting."
		}
	}
	return nil
}
func (e *Engine) Start(mode string, covers bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.snapshot.Run != nil || e.snapshot.Running {
		return errors.New("Resume or end the unfinished run first")
	}
	if mode != "new" && mode != "refresh" && mode != "retry" && mode != "covers" {
		return errors.New("Unknown run mode")
	}
	if mode != "covers" && !e.snapshot.ConnectionReady {
		return errors.New("Test the connection before starting")
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	phase := "discover"
	if mode == "retry" {
		phase = "fetch"
	}
	if mode == "covers" {
		phase = "covers"
	}
	run := store.Run{Source: e.net.Source(), ID: fmt.Sprintf("%x", b), Mode: mode, Phase: phase, State: "starting", Started: time.Now().UTC().Format(time.RFC3339Nano), Covers: covers}
	if err := e.store.CreateRun(run); err != nil {
		e.snapshot.State = "blocked_storage"
		e.snapshot.Message = "Cannot start a run in this data folder. Check space and permissions."
		return errors.New(e.snapshot.Message)
	}
	e.snapshot = Snapshot{
		State: "starting", Phase: phase, Run: &run, ConnectionReady: e.snapshot.ConnectionReady,
		Catalogued: e.snapshot.Catalogued, CataloguedCovers: e.snapshot.CataloguedCovers,
	}
	e.launchLocked()
	return nil
}
func (e *Engine) Resume() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.snapshot.Run == nil || e.snapshot.Running {
		return errors.New("No paused run to resume")
	}
	if e.snapshot.Run.Phase != "covers" && !e.snapshot.ConnectionReady {
		return errors.New("Test the connection before resuming")
	}
	e.launchLocked()
	return nil
}
func (e *Engine) launchLocked() {
	hard, cancelHard := context.WithCancel(context.Background())
	starts, cancelStarts := context.WithCancel(hard)
	e.cancelHard = cancelHard
	e.cancelStarts = cancelStarts
	e.done = make(chan struct{})
	e.end = false
	e.committedProducts = 0
	e.committedCovers = 0
	e.snapshot.Running = true
	e.snapshot.State = "starting"
	run := *e.snapshot.Run
	go e.execute(hard, starts, run)
}
func (e *Engine) Pause() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.snapshot.Running {
		return errors.New("No running crawl to pause")
	}
	e.snapshot.State = "pausing"
	e.snapshot.Message = "Finishing current requests and saving received results…"
	e.cancelStarts()
	return nil
}
func (e *Engine) End() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.snapshot.Run == nil {
		return nil
	}
	if e.snapshot.Running {
		e.end = true
		e.snapshot.State = "pausing"
		e.snapshot.Message = "Saving received results before ending the run…"
		e.cancelStarts()
		return nil
	}
	if len(e.pending) > 0 {
		return errors.New("Resume to save the retained batch before ending this run")
	}
	if err := e.store.UpdateRun(e.snapshot.Run.ID, e.snapshot.Phase, "ended", true); err != nil {
		return errors.New("Cannot finish the run; check local storage")
	}
	e.snapshot.Run = nil
	e.discovery = nil
	e.snapshot.State = "ready"
	if !e.snapshot.ConnectionReady {
		e.snapshot.State = "setup"
	}
	e.snapshot.Message = "Run ended. Committed records and covers are retained."
	return nil
}
func (e *Engine) Shutdown() {
	e.mu.Lock()
	if !e.snapshot.Running {
		e.mu.Unlock()
		return
	}
	done := e.done
	e.cancelStarts()
	e.cancelHard()
	e.mu.Unlock()
	<-done
}
func (e *Engine) Wait(ctx context.Context) error {
	e.mu.Lock()
	done := e.done
	running := e.snapshot.Running
	e.mu.Unlock()
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
func (e *Engine) Failures(offset int) ([]store.Failure, error) {
	return e.store.Failures(offset, 50, e.net.Source())
}

func (e *Engine) setPhase(run *store.Run, phase, state, message string) error {
	if err := e.store.UpdateRun(run.ID, phase, state, false); err != nil {
		return dtrpg.Fail("storage", "Could not save run state; check local storage")
	}
	run.Phase = phase
	run.State = state
	e.change(func(s *Snapshot) { s.Phase = phase; s.State = state; s.Message = message; copy := *run; s.Run = &copy })
	return nil
}
func (e *Engine) execute(hard, starts context.Context, run store.Run) {
	e.net.BeginRun(run.ID)
	start := time.Now()
	var err error
	defer func() {
		e.mu.Lock()
		ending := e.end
		e.mu.Unlock()
		state, message, finished := "completed", "Run complete. Records and selected covers are saved locally.", true
		if err != nil {
			f := dtrpg.FailureOf(err)
			state = "paused"
			message = f.Message
			finished = false
			if errors.Is(err, context.Canceled) {
				message = "Paused. Received results have been committed."
			}
			switch f.Kind {
			case "storage":
				state = "blocked_storage"
			case "authentication":
				state = "blocked_auth"
				e.change(func(s *Snapshot) { s.ConnectionReady = false })
			}
		}
		if ending && len(e.pending) == 0 {
			state = "ended"
			message = "Run ended. Committed data is retained."
			finished = true
		}
		v := e.Snapshot()
		if state == "completed" && (v.ProductErrors > 0 || v.CoverErrors > 0 || v.MetadataFailures > 0) {
			state = "completed_errors"
			message = "Finished with errors. Successful records and covers are saved."
		}
		if saveErr := e.store.UpdateRun(run.ID, run.Phase, state, finished); saveErr != nil {
			state = "blocked_storage"
			message = "Could not persist run state. Fix local storage and resume; committed data is safe."
			finished = false
		}
		e.mu.Lock()
		e.snapshot.State = state
		e.snapshot.Message = message
		e.snapshot.Running = false
		e.snapshot.ProductsPerSecond = 0
		e.snapshot.CoversPerSecond = 0
		if finished {
			e.snapshot.Run = nil
			e.discovery = nil
		} else {
			run.State = state
			e.snapshot.Run = &run
		}
		e.cancelHard()
		close(e.done)
		e.mu.Unlock()
	}()
	if len(e.pending) > 0 {
		err = e.flush(run.ID, run.Phase, start)
		if err != nil {
			return
		}
	}
	if run.Phase != "covers" {
		var ids []int64
		if run.Mode != "retry" {
			if err = e.setPhase(&run, "discover", "discovering", "Reading declared sitemaps; total is not yet known."); err != nil {
				return
			}
			if e.discovery == nil {
				e.discovery = &dtrpg.DiscoveryState{}
			}
			ids, err = e.net.DiscoverWithState(starts, e.discovery, func(v dtrpg.Discovery) {
				e.change(func(s *Snapshot) {
					s.Found = v.Found
					s.Sitemaps = v.Sitemaps
					s.SitemapFallback = v.Fallback
					s.SitemapsSkipped = v.Skipped
				})
			})
			if err != nil {
				return
			}
		} else {
			_, err = e.net.Robots(starts)
			if err != nil {
				return
			}
		}
		ids, err = e.store.Eligibility(run, ids)
		if err != nil {
			err = dtrpg.Fail("storage", "Could not read eligible products")
			return
		}
		saved, failed, countErr := e.store.RunCounts(run.ID)
		if countErr != nil {
			err = dtrpg.Fail("storage", "Could not read run progress")
			return
		}
		warned, metadataFailed, countErr := e.store.RunMetadataCounts(starts, run.ID)
		if countErr != nil {
			err = dtrpg.Fail("storage", "Could not read metadata progress")
			return
		}
		e.change(func(s *Snapshot) {
			s.Saved = saved
			s.ProductErrors = failed
			s.MetadataWarnings, s.MetadataFailures = warned, metadataFailed
			s.Eligible = len(ids) + saved + failed
			s.Queued = len(ids)
		})
		if err = e.setPhase(&run, "fetch", "fetching", "Saving complete product responses to this computer."); err != nil {
			return
		}
		err = e.fetch(hard, starts, run, ids, time.Now())
		if err != nil {
			return
		}
		if starts.Err() != nil {
			err = starts.Err()
			return
		}
		if !run.Covers {
			return
		}
	}
	if starts.Err() != nil {
		err = starts.Err()
		return
	}
	if err = e.setPhase(&run, "covers", "covers", "Downloading missing or changed primary covers."); err != nil {
		return
	}
	err = e.covers(hard, starts, run, time.Now())
}
func (e *Engine) flush(runID, phase string, start time.Time) error {
	if len(e.pending) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	v, err := e.store.Commit(ctx, runID, e.pending)
	if err != nil {
		return dtrpg.Fail("storage", "Could not commit results. The current batch is retained in memory; fix storage and resume.")
	}
	clear(e.pending)
	e.pending = e.pending[:0]
	e.committedProducts += v.Products
	e.committedCovers += v.Covers
	e.change(func(s *Snapshot) {
		s.Catalogued += v.Added
		s.CataloguedCovers += v.AddedCovers
		s.Saved += v.Products
		s.MetadataWarnings += v.MetadataWarnings
		s.MetadataFailures += v.MetadataFailures
		s.CoverSaved += v.Covers
		if phase == "covers" || phase == "cover" {
			s.CoverErrors += v.Failures
		} else {
			s.ProductErrors += v.Failures
		}
		s.CommitSeconds += v.Duration.Seconds()
		elapsed := time.Since(start).Seconds()
		if phase == "cover" || phase == "covers" {
			s.CoversPerSecond = float64(e.committedCovers) / elapsed
		} else {
			s.ProductsPerSecond = float64(e.committedProducts) / elapsed
		}
	})
	return nil
}

type outcome struct {
	result       store.Result
	err          error
	http, decode time.Duration
}

func (e *Engine) fetch(hard, starts context.Context, run store.Run, ids []int64, start time.Time) error {
	jobs := make(chan int64)
	results := make(chan outcome)
	var wg sync.WaitGroup
	for worker := 0; worker < e.config.Workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				r, err := e.net.GetForRun(hard, starts, e.net.Source().ProductURL(id), id, dtrpg.ProductLimit, false, false)
				o := outcome{result: store.Result{ID: id}, err: err, http: r.Duration}
				if err == nil {
					decode := time.Now()
					_, err = dtrpg.Validate(r.Body, id)
					o.decode = time.Since(decode)
					o.err = err
					if err == nil {
						o.result.JSON = r.Body
					}
				}
				results <- o
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, id := range ids {
			if starts.Err() != nil {
				return
			}
			select {
			case <-starts.Done():
				return
			case jobs <- id:
				e.change(func(s *Snapshot) { s.Queued-- })
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()
	return e.collect(run, results, "product", start)
}
func (e *Engine) collect(run store.Run, results <-chan outcome, phase string, start time.Time) error {
	var blocked error
	bytes := 0
	var timer *time.Timer
	var tick <-chan time.Time
	stopTimer := func() {
		if timer != nil {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		tick = nil
	}
	defer stopTimer()
	flush := func() {
		if blocked != nil && dtrpg.FailureOf(blocked).Kind == "storage" {
			return
		}
		if err := e.flush(run.ID, phase, start); err != nil {
			blocked = err
			e.cancelStarts()
		}
		bytes = 0
		stopTimer()
	}
	for {
		select {
		case <-tick:
			flush()
		case o, ok := <-results:
			if !ok {
				flush()
				if blocked != nil {
					return blocked
				}
				return nil
			}
			e.change(func(s *Snapshot) { s.HTTPSeconds += o.http.Seconds(); s.DecodeSeconds += o.decode.Seconds() })
			if o.err != nil {
				if errors.Is(o.err, context.Canceled) || errors.Is(o.err, context.DeadlineExceeded) {
					if blocked == nil {
						blocked = context.Canceled
					}
					continue
				}
				f := dtrpg.FailureOf(o.err)
				if f.Kind == "authentication" || f.Kind == "storage" {
					if blocked == nil || dtrpg.FailureOf(blocked).Kind != "storage" {
						blocked = o.err
					}
					e.cancelStarts()
					continue
				}
				o.result.Failure = &store.Failure{ID: o.result.ID, Phase: phase, Kind: f.Kind, Status: f.Status, Message: f.Message}
			}
			// A failed persistence batch is retained. Later worker results are replayed
			// from their uncommitted IDs when resumed, rather than growing memory.
			if blocked != nil && dtrpg.FailureOf(blocked).Kind == "storage" {
				continue
			}
			if len(e.pending) > 0 && bytes+len(o.result.JSON) > e.config.BatchBytes {
				flush()
				if blocked != nil && dtrpg.FailureOf(blocked).Kind == "storage" {
					continue
				}
			}
			e.pending = append(e.pending, o.result)
			bytes += len(o.result.JSON)
			if len(e.pending) == 1 {
				if timer == nil {
					timer = time.NewTimer(e.config.FlushInterval)
				} else {
					timer.Reset(e.config.FlushInterval)
				}
				tick = timer.C
			}
			if len(e.pending) >= e.config.BatchCount || bytes >= e.config.BatchBytes {
				flush()
			}
		}
	}
}

func (e *Engine) covers(hard, starts context.Context, run store.Run, start time.Time) error {
	type job struct {
		id  int64
		url string
	}
	jobs := make(chan job)
	results := make(chan outcome)
	scanDone := make(chan error, 1)
	var wg sync.WaitGroup
	e.change(func(s *Snapshot) {
		s.CoverTotal = 0
		s.CoverSaved = 0
		s.CoverErrors = 0
		s.CoverScanned = 0
		s.CoverScanDone = false
	})
	for worker := 0; worker < e.config.CoverWorkers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				path, err := e.net.DownloadCover(hard, starts, e.store.Dir, j.id, j.url)
				o := outcome{result: store.Result{ID: j.id}, err: err}
				if err == nil {
					o.result.Cover = &store.Cover{URL: j.url, Path: path}
				}
				results <- o
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(jobs)
		var after int64
		for {
			if starts.Err() != nil {
				scanDone <- starts.Err()
				return
			}
			page, err := e.store.CoverPage(after, e.net.Source())
			if err != nil {
				scanDone <- dtrpg.Fail("storage", "Could not scan saved products for covers")
				return
			}
			if len(page) == 0 {
				e.change(func(s *Snapshot) { s.CoverScanDone = true })
				scanDone <- nil
				return
			}
			for _, r := range page {
				after = r.ID
				e.change(func(s *Snapshot) { s.CoverScanned++ })
				v, err := dtrpg.Validate(r.JSON, r.ID)
				if err != nil {
					scanDone <- dtrpg.Fail("storage", "Saved product response failed validation")
					return
				}
				if v.Data.Attributes.Image == nil || *v.Data.Attributes.Image == "" {
					continue
				}
				source, err := dtrpg.ImageURL(*v.Data.Attributes.Image)
				if err == nil && source == r.URL && dtrpg.CoverExists(e.store.Dir, r.Path) {
					continue
				}
				e.change(func(s *Snapshot) { s.CoverTotal++ })
				if err != nil {
					results <- outcome{result: store.Result{ID: r.ID}, err: err}
					continue
				}
				select {
				case <-starts.Done():
					scanDone <- starts.Err()
					return
				case jobs <- job{r.ID, source}:
				}
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()
	err := e.collect(run, results, "cover", start)
	scanErr := <-scanDone
	if err != nil {
		return err
	}
	return scanErr
}
