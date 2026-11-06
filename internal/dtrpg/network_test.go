package dtrpg

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"github.com/Rainbows4Blood/Nekonomicon/internal/auth"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

type mapped struct {
	base *url.URL
	t    http.RoundTripper
}

func (m mapped) RoundTrip(r *http.Request) (*http.Response, error) {
	c := r.Clone(r.Context())
	u := *r.URL
	c.URL = &u
	c.URL.Scheme = m.base.Scheme
	c.URL.Host = m.base.Host
	c.Host = r.URL.Host
	return m.t.RoundTrip(c)
}
func fixture(t *testing.T, h http.HandlerFunc) *Network {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	u, _ := url.Parse(s.URL)
	c, _ := auth.Manual("web=synthetic", "api=synthetic")
	n := NewNetwork(c, mapped{u, s.Client().Transport})
	n.Attempts = 1
	return n
}
func TestDiscoveryAndCompatibility(t *testing.T) {
	n := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != UserAgent {
			t.Error("compatibility UA changed")
		}
		if r.Host == "api.drivethrurpg.com" && r.Header.Get("Cookie") != "api=synthetic" {
			t.Error("API cookie scope")
		}
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprint(w, "User-agent: *\nAllow: /\nSitemap: https://www.drivethrurpg.com/index.xml\n")
		case "/index.xml":
			fmt.Fprint(w, `<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><sitemap><loc>https://www.drivethrurpg.com/child.xml.gz</loc></sitemap><sitemap><loc>https://www.drivethrurpg.com/index.xml</loc></sitemap></sitemapindex>`)
		case "/child.xml.gz":
			g := gzip.NewWriter(w)
			fmt.Fprint(g, `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:image="urn:image"><url><loc>https://www.drivethrurpg.com/en/product/42/slug</loc><image:loc>https://www.drivethrurpg.com/product/99</image:loc></url><url><loc>https://www.drivethrurpg.com/product_info.php?products_id=42&amp;x=y</loc></url><url><loc>https://evil.invalid/product/99</loc></url></urlset>`)
			g.Close()
		default:
			t.Error("unexpected path")
		}
	})
	ids, e := n.Discover(context.Background(), nil)
	if e != nil || len(ids) != 1 || ids[0] != 42 {
		t.Fatalf("%v %v", ids, e)
	}
}
func TestSitemapCompletenessAndMapping(t *testing.T) {
	for _, s := range []string{`<urlset><url>`, `<urlset></urlset><urlset/>`, `<!DOCTYPE foo><urlset/>`, `<urlset><url/></urlset>`, `<urlset/>extra`} {
		if _, e := ParseSitemap([]byte(s), 10000); e == nil {
			t.Errorf("accepted %s", s)
		}
	}
	if _, e := ParseSitemap([]byte(`<urlset/>`), 2); e == nil {
		t.Fatal("size ignored")
	}
	var zipped bytes.Buffer
	g := gzip.NewWriter(&zipped)
	io.WriteString(g, strings.Repeat(" ", 1000))
	g.Close()
	if _, e := ParseSitemap(zipped.Bytes(), 100); e == nil {
		t.Fatal("gzip size ignored")
	}
	for _, s := range []string{"https://www.drivethrurpg.com/en/product/12/name", "https://drivethrurpg.com/product/12", "https://www.drivethrurpg.com/product_info.php?products_id=12"} {
		if ProductID(s) != 12 {
			t.Error(s)
		}
	}
	for _, s := range []string{"https://evil.invalid/product/12", "http://www.drivethrurpg.com/product/12", "https://www.drivethrurpg.com/product/-1", "https://www.drivethrurpg.com/product_info.php?products_id=1&products_id=2"} {
		if ProductID(s) != 0 {
			t.Error(s)
		}
	}
}
func TestResponseClassification(t *testing.T) {
	for _, x := range []struct {
		status     int
		body, kind string
	}{{403, `{"accessDenied":true}`, "restricted"}, {403, `{"sessionExpired":true}`, "authentication"}, {403, `<title>Just a moment...</title>`, "authentication"}, {403, `Access denied`, "restricted"}, {200, `{"ageGate":true}`, "restricted"}, {200, `<h1>Verify you are human</h1>`, "authentication"}, {404, ``, "missing"}, {429, ``, "transient"}, {200, `<html><script>session expired</script><h1>Books</h1></html>`, ""}} {
		f := classify(x.status, []byte(x.body), true)
		if x.kind == "" {
			if f != nil {
				t.Error(f)
			}
		} else if f == nil || f.Kind != x.kind {
			t.Errorf("%d %s: %+v", x.status, x.body, f)
		}
	}
	b, e := os.ReadFile("../../docs/fixtures/dtrpg-product-584455.json")
	if e != nil {
		t.Fatal(e)
	}
	v, e := Validate(b, 584455)
	if e != nil || v.Data.Attributes.Image == nil {
		t.Fatal(e)
	}
	if _, e = Validate(b, 1); e == nil {
		t.Fatal("wrong ID accepted")
	}
	for _, s := range []string{`{}`, `{"error":"no"}`, `<html>hello</html>`, `{"data":{"type":"Other","attributes":{"productId":1}}}`} {
		if _, e = Validate([]byte(s), 1); e == nil {
			t.Error("invalid product accepted")
		}
	}
}
func TestRedirectAndCoverCredentialIsolation(t *testing.T) {
	requests := 0
	n := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Host == "d1vzi28wh99zvq.cloudfront.net" {
			if r.Header.Get("Cookie") != "" {
				t.Error("cookies leaked to CDN")
			}
			fmt.Fprint(w, "image")
			return
		}
		w.Header().Set("Location", "https://evil.invalid/stolen")
		w.WriteHeader(302)
	})
	if _, e := n.Get(context.Background(), auth.Website+"robots.txt", 0, 1024, true, false); e == nil {
		t.Fatal("foreign redirect allowed")
	}
	if requests != 1 {
		t.Fatal("redirect followed")
	}
	if _, e := n.Get(context.Background(), CDN+"cover.png", 0, 1024, false, true); e != nil {
		t.Fatal(e)
	}
}

func TestRateLimitAndCancellation(t *testing.T) {
	attempts := 0
	n := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			return
		}
		fmt.Fprint(w, "ok")
	})
	n.Attempts = 2
	start := time.Now()
	if _, e := n.Get(context.Background(), CDN+"image.png", 0, 1024, false, true); e != nil {
		t.Fatal(e)
	}
	if attempts != 2 || time.Since(start) < time.Second {
		t.Fatal("Retry-After was ignored")
	}
	n = fixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(429)
	})
	n.Attempts = 3
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start = time.Now()
	if _, e := n.Get(ctx, CDN+"image.png", 0, 1024, false, true); e == nil || time.Since(start) > time.Second {
		t.Fatal("retry wait did not cancel")
	}
}

func TestFallbackStillEnforcesRobotsAndIncompleteDiscovery(t *testing.T) {
	for _, blocked := range []bool{true, false} {
		n := fixture(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/robots.txt" {
				if blocked {
					fmt.Fprint(w, "User-agent: *\nDisallow: /sitemap.xml\n")
				} else {
					fmt.Fprint(w, "User-agent: *\nAllow: /\n")
				}
				return
			}
			fmt.Fprint(w, `<urlset><url><loc>https://www.drivethrurpg.com/product/1</loc></url>`)
		})
		var fallback bool
		if ids, e := n.Discover(context.Background(), func(v Discovery) { fallback = v.Fallback }); e == nil || len(ids) != 0 || !fallback {
			t.Fatal("fallback bypassed robots or incomplete XML was accepted")
		}
	}
}

func TestFlatAndErrorEnvelopes(t *testing.T) {
	for _, body := range []string{`{"productId":7,"image":null,"unknown":true}`, `{"productId":7,"publisherId":1}`} {
		if _, err := Validate([]byte(body), 7); err != nil {
			t.Fatal(err)
		}
	}
	for _, body := range []string{`{"productId":7}`, `{"productId":8,"image":null}`, `{"productId":7,"image":null,"errors":[]}`, `{"data":{"type":"Product","attributes":{"productId":7}},"error":"failure"}`} {
		if _, err := Validate([]byte(body), 7); err == nil {
			t.Fatal("invalid envelope accepted")
		}
	}
}
