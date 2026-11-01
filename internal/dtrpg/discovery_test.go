package dtrpg

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

func TestRobotsExcludedSitemapDoesNotStopDiscovery(t *testing.T) {
	requests := map[string]int{}
	n := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		requests[r.URL.Path]++
		switch r.URL.Path {
		case "/robots.txt":
			// The live prefix excludes publishers_sitemap.xml as well as /publish.
			fmt.Fprint(w, "User-agent: *\nDisallow: /publish\n")
		case "/sitemap.xml":
			fmt.Fprint(w, `<sitemapindex><sitemap><loc>https://www.drivethrurpg.com/products.xml</loc></sitemap><sitemap><loc>https://www.drivethrurpg.com/publishers_sitemap.xml</loc></sitemap><sitemap><loc>https://www.drivethrurpg.com/remaining.xml</loc></sitemap></sitemapindex>`)
		case "/products.xml":
			fmt.Fprint(w, `<urlset><url><loc>https://www.drivethrurpg.com/product/1</loc></url></urlset>`)
		case "/remaining.xml":
			fmt.Fprint(w, `<urlset><url><loc>https://www.drivethrurpg.com/product/2</loc></url></urlset>`)
		default:
			t.Error("excluded sitemap reached the server")
		}
	})
	var last Discovery
	ids, err := n.Discover(context.Background(), func(v Discovery) { last = v })
	if err != nil || !reflect.DeepEqual(ids, []int64{1, 2}) {
		t.Fatalf("allowed products lost: %v %v", ids, err)
	}
	if last.Skipped != 1 || last.Sitemaps != 3 || last.Found != 2 || !last.Fallback || requests["/publishers_sitemap.xml"] != 0 {
		t.Fatalf("incorrect policy outcome: %+v, requests %v", last, requests)
	}
}

func TestDiscoveryRetainsFrontierAfterPauseAndFailure(t *testing.T) {
	for _, cancelDiscovery := range []bool{true, false} {
		t.Run(fmt.Sprintf("cancel=%t", cancelDiscovery), func(t *testing.T) {
			requests := map[string]int{}
			n := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				requests[r.URL.Path]++
				switch r.URL.Path {
				case "/robots.txt":
					fmt.Fprint(w, "User-agent: *\nAllow: /\n")
				case "/sitemap.xml":
					fmt.Fprint(w, `<sitemapindex><sitemap><loc>https://www.drivethrurpg.com/one.xml</loc></sitemap><sitemap><loc>https://www.drivethrurpg.com/two.xml</loc></sitemap></sitemapindex>`)
				case "/one.xml":
					fmt.Fprint(w, `<urlset><url><loc>https://www.drivethrurpg.com/product/1</loc></url></urlset>`)
				case "/two.xml":
					if !cancelDiscovery && requests[r.URL.Path] == 1 {
						fmt.Fprint(w, `<urlset><url>`)
						return
					}
					fmt.Fprint(w, `<urlset><url><loc>https://www.drivethrurpg.com/product/2</loc></url><url><loc>https://www.drivethrurpg.com/product/1</loc></url></urlset>`)
				}
			})
			state := &DiscoveryState{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ids, err := n.DiscoverWithState(ctx, state, func(v Discovery) {
				if cancelDiscovery && v.Found == 1 {
					cancel()
				}
			})
			if err == nil || len(ids) != 0 || cancelDiscovery && !errors.Is(err, context.Canceled) {
				t.Fatalf("partial discovery escaped: %v %v", ids, err)
			}
			var resumed []Discovery
			ids, err = n.DiscoverWithState(context.Background(), state, func(v Discovery) { resumed = append(resumed, v) })
			if err != nil || !reflect.DeepEqual(ids, []int64{1, 2}) || resumed[0].Found != 1 || resumed[0].Sitemaps != 2 {
				t.Fatalf("frontier was lost: %v %v %+v", ids, err, resumed)
			}
			if requests["/sitemap.xml"] != 1 || requests["/one.xml"] != 1 {
				t.Fatalf("completed maps repeated: %v", requests)
			}
			// A fetch-phase pause can reuse complete discovery too.
			_, err = n.DiscoverWithState(context.Background(), state, nil)
			wantTwo := 1
			if !cancelDiscovery {
				wantTwo = 2
			}
			if err != nil || requests["/two.xml"] != wantTwo || requests["/robots.txt"] != 6 {
				t.Fatalf("completed discovery repeated or policy not refreshed: %v %v", requests, err)
			}
		})
	}
}
