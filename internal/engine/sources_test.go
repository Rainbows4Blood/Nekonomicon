package engine

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Rainbows4Blood/Nekonomicon/internal/auth"
	"github.com/Rainbows4Blood/Nekonomicon/internal/dtrpg"
	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

func TestBothStorefrontsEndToEndInOneCatalog(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var img bytes.Buffer
	if err = png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	for _, source := range []storefront.Source{storefront.DTRPG, storefront.DMSGuild} {
		t.Run(string(source), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/robots.txt":
					fmt.Fprint(w, "User-agent: *\nAllow: /\n")
				case "/":
					fmt.Fprint(w, "<html>Store</html>")
				case "/sitemap.xml":
					fmt.Fprintf(w, `<urlset><url><loc>%sen/product/42/name</loc></url><url><loc>%sproduct_info.php?products_id=42</loc></url><url><loc>https://foreign.invalid/product/99</loc></url></urlset>`, source.Website(), source.Website())
				case "/api/vBeta/products/42":
					requests++
					if source == storefront.DMSGuild && r.URL.RawQuery != "" {
						t.Error("DTRPG query leaked into DM's Guild")
					}
					fmt.Fprintf(w, ` {"data":{"id":"/api/vBeta/products/42","type":"Product","attributes":{"productId":42,"image":"123/42.png"}},"included":[{"store":"%s"}]} `, source)
				case "/images/123/42.png":
					if r.Header.Get("Cookie") != "" {
						t.Error("CDN received credentials")
					}
					w.Header().Set("Content-Type", "image/png")
					w.Write(img.Bytes())
				default:
					t.Error("unexpected request", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			u, _ := url.Parse(server.URL)
			cookies, _ := auth.Manual("session=synthetic", "session=synthetic", source)
			n := dtrpg.NewNetwork(cookies, redirectTransport{u, server.Client().Transport}, source)
			if err = n.Test(context.Background(), 42); err != nil {
				t.Fatal(err)
			}
			e, err := New(s, n, DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			defer e.Shutdown()
			if err = e.SetReady(true); err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"new", "new", "refresh", "covers"} {
				if err = e.Start(mode, true); err != nil {
					t.Fatal(err)
				}
				deadline := time.Now().Add(5 * time.Second)
				for e.Snapshot().Running && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				v := e.Snapshot()
				if v.State != "completed" || v.Catalogued != 1 || v.CataloguedCovers != 1 {
					t.Fatalf("%s: %+v", mode, v)
				}
			}
			if requests != 3 {
				t.Fatal("new/cover runs fetched saved products", requests)
			}
			b, err := s.Response(42, source)
			if err != nil || !strings.Contains(string(b), string(source)) {
				t.Fatal("wrong saved bytes", err)
			}
			page, err := s.CoverPage(0, source)
			if err != nil || len(page) != 1 || !strings.HasPrefix(page[0].Path, "covers/"+string(source)+"/") {
				t.Fatal("wrong cover namespace", page, err)
			}
		})
	}
}

func TestResumeRejectsWrongStorefront(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.CreateRun(store.Run{ID: "guild", Source: storefront.DMSGuild, Mode: "refresh", Phase: "fetch", State: "paused"}); err != nil {
		t.Fatal(err)
	}
	if _, err = New(s, dtrpg.NewNetwork(auth.Cookies{}, nil), DefaultConfig()); err == nil {
		t.Fatal("wrong storefront resumed")
	}
	e, err := New(s, dtrpg.NewNetwork(auth.Cookies{}, nil, storefront.DMSGuild), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if e.Snapshot().Run.Source != storefront.DMSGuild || e.Snapshot().ConnectionReady {
		t.Fatal("incorrect restored connection/run")
	}
}
