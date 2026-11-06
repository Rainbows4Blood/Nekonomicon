// measure exercises the actual three-phase engine against a local HTTP server.
// All responses and image pixels are synthetic or from the supplied fixture.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/Rainbows4Blood/Nekonomicon/internal/auth"
	"github.com/Rainbows4Blood/Nekonomicon/internal/dtrpg"
	"github.com/Rainbows4Blood/Nekonomicon/internal/engine"
	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"
)

type transport struct {
	base *url.URL
	t    http.RoundTripper
}

func (m transport) RoundTrip(r *http.Request) (*http.Response, error) {
	c := r.Clone(r.Context())
	u := *r.URL
	c.URL = &u
	c.URL.Scheme = m.base.Scheme
	c.URL.Host = m.base.Host
	return m.t.RoundTrip(c)
}
func main() {
	count := flag.Int("count", 1000, "synthetic products (1..10000)")
	flag.Parse()
	if *count < 1 || *count > 10000 {
		panic("count outside limit")
	}
	if err := measure(*count); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func measure(count int) error {
	fixture, err := os.ReadFile("docs/fixtures/dtrpg-product-584455.json")
	if err != nil {
		return err
	}
	im := image.NewRGBA(image.Rect(0, 0, 128, 128))
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			im.Set(x, y, color.RGBA{uint8(x * y), uint8(x*13 + y*31), uint8(x*37 - y*19), 255})
		}
	}
	var pngBytes bytes.Buffer
	png.Encode(&pngBytes, im)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/robots.txt":
			fmt.Fprint(w, "User-agent: *\nAllow: /\nSitemap: https://www.drivethrurpg.com/sitemap.xml\n")
		case r.URL.Path == "/sitemap.xml":
			fmt.Fprint(w, "<urlset>")
			for i := 1; i <= count; i++ {
				fmt.Fprintf(w, "<url><loc>https://www.drivethrurpg.com/product/%d</loc></url>", i)
			}
			fmt.Fprint(w, "</urlset>")
		case strings.HasPrefix(r.URL.Path, "/api/vBeta/products/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/vBeta/products/")
			w.Header().Set("Content-Type", "application/json")
			w.Write(bytes.ReplaceAll(fixture, []byte("584455"), []byte(id)))
		default:
			w.Header().Set("Content-Type", "image/png")
			w.Write(pngBytes.Bytes())
		}
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	n := dtrpg.NewNetwork(auth.Cookies{}, transport{u, server.Client().Transport})
	dir, err := os.MkdirTemp("", "nekonomicon-measure-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	s, err := store.Open(dir)
	if err != nil {
		return err
	}
	defer s.Close()
	e, err := engine.New(s, n, engine.DefaultConfig())
	if err != nil {
		return err
	}
	e.SetReady(true)
	start := time.Now()
	if err = e.Start("refresh", true); err != nil {
		return err
	}
	var peakHeap uint64
	for e.Snapshot().Running {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		peakHeap = max(peakHeap, m.HeapAlloc)
		time.Sleep(25 * time.Millisecond)
	}
	e.Wait(context.Background())
	v := e.Snapshot()
	if v.State != "completed" {
		return fmt.Errorf("measurement ended %s: %s", v.State, v.Message)
	}
	out := map[string]any{"go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "logicalCPUs": runtime.NumCPU(), "products": count, "fixtureBytes": len(fixture), "coverBytes": pngBytes.Len(), "productWorkers": 8, "coverWorkers": 4, "batchCount": 1000, "batchBytes": 32 << 20, "durability": "WAL / synchronous=NORMAL", "elapsedSeconds": time.Since(start).Seconds(), "httpSecondsSum": v.HTTPSeconds, "decodeSecondsSum": v.DecodeSeconds, "commitSeconds": v.CommitSeconds, "peakSampledGoHeapBytes": peakHeap, "catalogued": v.Catalogued, "coversSaved": v.CoverSaved, "includes": "local HTTP, decode, discovery, SQLite, cover downloads and atomic file writes", "excludes": "Internet latency, browser import, desktop UI; heap measurement is not process RSS"}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
	return nil
}
