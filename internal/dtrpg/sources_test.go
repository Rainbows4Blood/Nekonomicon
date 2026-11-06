package dtrpg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Rainbows4Blood/Nekonomicon/internal/auth"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

func TestDMGuildRejectsCrossStoreRequestsAndRedirects(t *testing.T) {
	source := storefront.DMSGuild
	for _, raw := range []string{"https://www.dmsguild.com/en/product/42/name", "https://dmsguild.com/product/42", "https://www.dmsguild.com/product_info.php?products_id=42"} {
		if ProductID(raw, source) != 42 || ProductID(raw) != 0 {
			t.Fatal("product mapping crossed storefronts")
		}
	}
	for _, raw := range []string{"https://www.drivethrurpg.com/product/42", "https://www.dmsguild.com.invalid/product/42", "http://www.dmsguild.com/product/42", "https://api.dmsguild.com/product/42"} {
		if ProductID(raw, source) != 0 {
			t.Fatal("unapproved product accepted")
		}
	}
	if SitemapURL("https://www.drivethrurpg.com/sitemap.xml", source) != "" {
		t.Fatal("foreign sitemap accepted")
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Host != "www.dmsguild.com" || r.Header.Get("Cookie") != "web=synthetic" {
			t.Error("unexpected host/cookie")
		}
		http.Redirect(w, r, "https://www.drivethrurpg.com/robots.txt", http.StatusFound)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	c, _ := auth.Manual("web=synthetic", "api=synthetic", source)
	n := NewNetwork(c, mapped{u, server.Client().Transport}, source)
	if _, err := n.Get(context.Background(), source.Website()+"robots.txt", 0, 1024, true, false); err == nil || FailureOf(err).Kind != "authentication" {
		t.Fatal("cross-store redirect accepted", err)
	}
	if _, err := n.Get(context.Background(), auth.Website+"robots.txt", 0, 1024, true, false); err == nil || FailureOf(err).Kind != "scope" {
		t.Fatal("cross-store request accepted", err)
	}
	if requests != 1 {
		t.Fatal("foreign host received a request")
	}
}
