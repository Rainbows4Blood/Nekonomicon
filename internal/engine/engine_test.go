package engine

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Rainbows4Blood/Nekonomicon/internal/auth"
	"github.com/Rainbows4Blood/Nekonomicon/internal/dtrpg"
	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
)

type redirectTransport struct {
	base *url.URL
	t    http.RoundTripper
}

func (m redirectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c := r.Clone(r.Context())
	u := *r.URL
	c.URL = &u
	c.URL.Scheme = m.base.Scheme
	c.URL.Host = m.base.Host
	return m.t.RoundTrip(c)
}
func setup(t *testing.T, handler http.HandlerFunc) (*Engine, *store.Store) {
	t.Helper()
	return setupRoutes(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprint(w, "User-agent: *\nAllow: /\nSitemap: https://www.drivethrurpg.com/sitemap.xml\n")
		case "/sitemap.xml":
			fmt.Fprint(w, `<urlset><url><loc>https://www.drivethrurpg.com/product/1</loc></url><url><loc>https://www.drivethrurpg.com/product/2</loc></url></urlset>`)
		default:
			handler(w, r)
		}
	})
}
func setupRoutes(t *testing.T, handler http.HandlerFunc) (*Engine, *store.Store) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	u, _ := url.Parse(server.URL)
	n := dtrpg.NewNetwork(auth.Cookies{}, redirectTransport{u, server.Client().Transport})
	n.Attempts = 1
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := DefaultConfig()
	c.Workers = 2
	c.FlushInterval = 50 * time.Millisecond
	e, err := New(s, n, c)
	if err != nil {
		t.Fatal(err)
	}
	e.SetReady(true)
	t.Cleanup(func() { e.Shutdown(); s.Close() })
	return e, s
}

func TestDiscoverySkipsRobotsAndResumeKeepsProgress(t *testing.T) {
	var roots, first, tail, products atomic.Int32
	held := make(chan struct{})
	e, _ := setupRoutes(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprint(w, "User-agent: *\nDisallow: /publish\n")
		case "/sitemap.xml":
			roots.Add(1)
			fmt.Fprint(w, `<sitemapindex><sitemap><loc>https://www.drivethrurpg.com/first.xml</loc></sitemap><sitemap><loc>https://www.drivethrurpg.com/publishers_sitemap.xml</loc></sitemap><sitemap><loc>https://www.drivethrurpg.com/tail.xml</loc></sitemap></sitemapindex>`)
		case "/first.xml":
			first.Add(1)
			fmt.Fprint(w, `<urlset><url><loc>https://www.drivethrurpg.com/product/1</loc></url></urlset>`)
		case "/publishers_sitemap.xml":
			t.Error("robots exclusion reached server")
		case "/tail.xml":
			if tail.Add(1) == 1 {
				close(held)
				<-r.Context().Done()
				return
			}
			fmt.Fprint(w, `<urlset><url><loc>https://www.drivethrurpg.com/product/2</loc></url></urlset>`)
		default:
			products.Add(1)
			body(w, strings.TrimPrefix(r.URL.Path, "/api/vBeta/products/"))
		}
	})
	if err := e.Start("new", false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-held:
	case <-time.After(3 * time.Second):
		t.Fatal("discovery stopped at robots exclusion")
	}
	if err := e.Pause(); err != nil {
		t.Fatal(err)
	}
	wait(t, e)
	v := e.Snapshot()
	if v.State != "paused" || v.Found != 1 || v.SitemapsSkipped != 1 || products.Load() != 0 {
		t.Fatalf("partial discovery outcome: %+v", v)
	}
	if err := e.Resume(); err != nil {
		t.Fatal(err)
	}
	wait(t, e)
	v = e.Snapshot()
	if v.State != "completed" || v.Catalogued != 2 || v.SitemapsSkipped != 1 || roots.Load() != 1 || first.Load() != 1 || tail.Load() != 2 {
		t.Fatalf("resume repeated discovery or failed to fetch: %+v, requests=%d/%d/%d", v, roots.Load(), first.Load(), tail.Load())
	}
	if err := e.Start("new", false); err != nil {
		t.Fatal(err)
	}
	wait(t, e)
	if roots.Load() != 2 || first.Load() != 2 || products.Load() != 2 {
		t.Fatal("new run reused old discovery or refetched saved products")
	}
}
func wait(t *testing.T, e *Engine) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}
func body(w http.ResponseWriter, id string) {
	fmt.Fprintf(w, `{"data":{"type":"Product","attributes":{"productId":%s,"image":null}},"included":[{"extra":"preserved"}]}`, id)
}
func TestPauseSettlesAndNewSkips(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var requests atomic.Int32
	e, s := setup(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		started <- struct{}{}
		<-release
		body(w, strings.TrimPrefix(r.URL.Path, "/api/vBeta/products/"))
	})
	if err := e.Start("refresh", false); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("requests not started")
		}
	}
	if err := e.Pause(); err != nil {
		t.Fatal(err)
	}
	if e.Snapshot().Catalogued != 0 {
		t.Fatal("counted before commit")
	}
	close(release)
	wait(t, e)
	v := e.Snapshot()
	if v.State != "paused" || v.Catalogued != 2 || v.Saved != 2 {
		t.Fatalf("pause: %+v", v)
	}
	r, _ := s.Active()
	if r == nil {
		t.Fatal("paused run closed")
	}
	if err := e.Resume(); err != nil {
		t.Fatal(err)
	}
	wait(t, e)
	if e.Snapshot().State != "completed" || requests.Load() != 2 {
		t.Fatal("resume refetched successes")
	}
	if err := e.Start("new", false); err != nil {
		t.Fatal(err)
	}
	wait(t, e)
	if requests.Load() != 2 {
		t.Fatal("new refetched saved products")
	}
}
func TestFailureRetryAndLastGood(t *testing.T) {
	var fail atomic.Bool
	e, s := setup(t, func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(403)
			fmt.Fprint(w, `{"accessDenied":true}`)
			return
		}
		body(w, strings.TrimPrefix(r.URL.Path, "/api/vBeta/products/"))
	})
	e.Start("new", false)
	wait(t, e)
	original, _ := s.Response(1)
	fail.Store(true)
	e.Start("refresh", false)
	wait(t, e)
	v := e.Snapshot()
	if v.State != "completed_errors" || v.ProductErrors != 2 || v.Catalogued != 2 {
		t.Fatalf("restricted products paused entire source: %+v", v)
	}
	saved, _ := s.Response(1)
	if string(saved) != string(original) {
		t.Fatal("last good overwritten")
	}
	fail.Store(false)
	e.Start("retry", false)
	wait(t, e)
	fs, _ := s.Failures(0, 10)
	if len(fs) != 0 || e.Snapshot().State != "completed" {
		t.Fatal("retry did not recover")
	}
}
func TestPersistenceFailureRetainsBatch(t *testing.T) {
	e, s := setup(t, func(w http.ResponseWriter, r *http.Request) {
		body(w, strings.TrimPrefix(r.URL.Path, "/api/vBeta/products/"))
	})
	db, err := sql.Open("sqlite", filepath.Join(s.Dir, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TRIGGER test_disk_failure BEFORE INSERT ON products BEGIN SELECT RAISE(FAIL,'synthetic disk full'); END`); err != nil {
		t.Fatal(err)
	}
	e.Start("refresh", false)
	wait(t, e)
	if e.Snapshot().State != "blocked_storage" || e.Snapshot().Catalogued != 0 {
		t.Fatalf("false commit: %+v", e.Snapshot())
	}
	db.Exec(`DROP TRIGGER test_disk_failure`)
	if err = e.Resume(); err != nil {
		t.Fatal(err)
	}
	wait(t, e)
	if e.Snapshot().Catalogued != 2 || e.Snapshot().State != "completed" {
		t.Fatalf("retained batch did not recover: %+v", e.Snapshot())
	}
}
func TestAuthenticationAndIncompleteDiscovery(t *testing.T) {
	e, _ := setup(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) })
	e.Start("new", false)
	wait(t, e)
	if e.Snapshot().State != "blocked_auth" || e.Snapshot().ConnectionReady {
		t.Fatal("session failure not blocked")
	}
	if err := e.Resume(); err == nil {
		t.Fatal("unvalidated auth resumed")
	}
}

func TestReopenedCoverRunCountsAndReusesSavedFiles(t *testing.T) {
	dir := t.TempDir()
	var img bytes.Buffer
	if err := png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	// Three covers were committed before shutdown. One file was removed
	// and one product now points to a different image.
	func() {
		s, err := store.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		run := store.Run{ID: "paused", Mode: "new", Phase: "covers", State: "paused", Covers: true}
		if err := s.CreateRun(run); err != nil {
			t.Fatal(err)
		}
		var batch []store.Result
		for id := int64(1); id <= 4; id++ {
			product := fmt.Sprintf(`{"productId":%d,"image":"%d.png"}`, id, id)
			batch = append(batch, store.Result{ID: id, JSON: []byte(product)})
			if id == 4 {
				continue // No previous cover record.
			}
			source := fmt.Sprintf("%simages/%d.png", dtrpg.CDN, id)
			if id == 3 {
				source = dtrpg.CDN + "images/old.png"
			}
			path := "covers/missing.png"
			if id != 2 {
				path, err = dtrpg.SaveCover(dir, id, source, img.Bytes(), "image/png")
				if err != nil {
					t.Fatal(err)
				}
			}
			batch = append(batch, store.Result{ID: id, Cover: &store.Cover{URL: source, Path: path}})
		}
		if _, err := s.Commit(context.Background(), run.ID, batch); err != nil {
			t.Fatal(err)
		}
	}()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/images/2.png", "/images/3.png", "/images/4.png":
			requests.Add(1)
		default:
			t.Errorf("unexpected request on cover resume: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(img.Bytes())
	}))
	defer server.Close()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	u, _ := url.Parse(server.URL)
	network := dtrpg.NewNetwork(auth.Cookies{}, redirectTransport{u, server.Client().Transport})
	e, err := New(s, network, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown()
	if v := e.Snapshot(); v.State != "paused" || v.CataloguedCovers != 3 || v.CoverScanned != 0 {
		t.Fatalf("before Resume: %+v", v)
	}
	if err := e.Resume(); err != nil {
		t.Fatal(err)
	}
	wait(t, e)
	if v := e.Snapshot(); v.State != "completed" || v.CataloguedCovers != 4 || v.CoverSaved != 3 || requests.Load() != 3 {
		t.Fatalf("after Resume: %+v, requests=%d", v, requests.Load())
	}
	if err := e.Start("covers", false); err != nil {
		t.Fatal(err)
	}
	wait(t, e)
	if v := e.Snapshot(); v.State != "completed" || v.CataloguedCovers != 4 || v.CoverTotal != 0 || requests.Load() != 3 {
		t.Fatalf("second pass: %+v, requests=%d", v, requests.Load())
	}
}

func TestTimerCommitsWhileAnotherRequestIsInFlight(t *testing.T) {
	held := make(chan struct{})
	release := make(chan struct{})
	e, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/vBeta/products/")
		if id == "2" {
			close(held)
			<-release
		}
		body(w, id)
	})
	if err := e.Start("new", false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-held:
	case <-time.After(3 * time.Second):
		t.Fatal("second request did not start")
	}
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for e.Snapshot().Catalogued != 1 {
		select {
		case <-deadline.C:
			close(release)
			t.Fatal("time threshold did not flush the first result")
		case <-ticker.C:
		}
	}
	if !e.Snapshot().Running {
		t.Fatal("run completed while request was still in flight")
	}
	_ = e.Pause()
	close(release)
	wait(t, e)
	if e.Snapshot().State != "paused" || e.Snapshot().Catalogued != 2 {
		t.Fatal("pause did not settle and flush")
	}
}
