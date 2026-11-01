package engine

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/Rainbows4Blood/Nekonomicon/internal/auth"
	"github.com/Rainbows4Blood/Nekonomicon/internal/dtrpg"
	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprint(w, "User-agent: *\nAllow: /\nSitemap: https://www.drivethrurpg.com/sitemap.xml\n")
		case "/sitemap.xml":
			fmt.Fprint(w, `<urlset><url><loc>https://www.drivethrurpg.com/product/1</loc></url><url><loc>https://www.drivethrurpg.com/product/2</loc></url></urlset>`)
		default:
			handler(w, r)
		}
	}))
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
