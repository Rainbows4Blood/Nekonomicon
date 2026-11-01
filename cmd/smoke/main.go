// smoke performs a deliberately bounded, opt-in protocol check. It prints only
// timings, counts and sanitized outcomes, never cookies or product responses.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/Rainbows4Blood/Nekonomicon/internal/auth"
	"github.com/Rainbows4Blood/Nekonomicon/internal/dtrpg"
	"os"
	"time"
)

func main() {
	profile := flag.String("profile", "", "Firefox profile name (required)")
	live := flag.Bool("live", false, "allow a bounded live DTRPG check")
	flag.Parse()
	if !*live || *profile == "" {
		fmt.Fprintln(os.Stderr, "Use -live -profile NAME for at most 8 sitemaps and 3 products")
		os.Exit(2)
	}
	if e := run(*profile); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run(name string) error {
	var p *auth.Profile
	for _, v := range auth.Profiles() {
		if v.Name == name {
			v := v
			p = &v
			break
		}
	}
	if p == nil {
		return fmt.Errorf("Firefox profile not found")
	}
	c, w, e := auth.Import(*p, 0)
	if e != nil {
		return e
	}
	for _, msg := range w {
		fmt.Println(msg)
	}
	fmt.Printf("Imported %d scoped cookies (values withheld)\n", len(c.Imported))
	n := dtrpg.NewNetwork(c, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	start := time.Now()
	if e = n.Test(ctx, 584455); e != nil {
		if dtrpg.FailureOf(e).Kind == "schema" {
			r, err := n.Get(ctx, dtrpg.ProductURL(584455), 584455, dtrpg.ProductLimit, false, false)
			if err == nil {
				var shape struct {
					Data struct {
						Type       string
						Attributes struct {
							ProductID int64 `json:"productId"`
						}
					}
					Errors json.RawMessage
				}
				if json.Unmarshal(r.Body, &shape) == nil {
					label := shape.Data.Type
					for _, ch := range label {
						if ch < 'A' || ch > 'Z' && ch < 'a' || ch > 'z' {
							label = "[non-label]"
							break
						}
					}
					if len(label) > 40 {
						label = "[long label]"
					}
					fmt.Printf("Safe schema diagnostic: type=%q, expected productId=%t, errors present=%t, %d bytes\n", label, shape.Data.Attributes.ProductID == 584455, len(shape.Errors) > 0, len(r.Body))
				}
				var top map[string]json.RawMessage
				_ = json.Unmarshal(r.Body, &top)
				for _, key := range []string{"data", "attributes", "productId", "type", "body", "result", "response", "product", "included", "products", "status", "message"} {
					raw, exists := top[key]
					if exists {
						var sub map[string]json.RawMessage
						_ = json.Unmarshal(raw, &sub)
						fmt.Printf("Shape: %s present; nested data=%t, attributes=%t, productId=%t\n", key, sub["data"] != nil, sub["attributes"] != nil, sub["productId"] != nil)
					}
				}
			}
		}
		return e
	}
	fmt.Printf("Website/API connection passed in %s\n", time.Since(start).Round(time.Millisecond))
	q, e := n.Robots(ctx)
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	ids := map[int64]bool{}
	for len(q) > 0 && len(seen) < 8 && len(ids) < 3 {
		u := q[0]
		q = q[1:]
		if seen[u] {
			continue
		}
		seen[u] = true
		r, e := n.Get(ctx, u, 0, dtrpg.SitemapLimit, false, false)
		if e != nil {
			return e
		}
		entries, e := dtrpg.ParseSitemap(r.Body, dtrpg.SitemapLimit)
		if e != nil {
			return e
		}
		for _, x := range entries {
			if x.Sitemap {
				if u := dtrpg.SitemapURL(x.Location); u != "" {
					q = append(q, u)
				}
			} else if id := dtrpg.ProductID(x.Location); id > 0 {
				ids[id] = true
				if len(ids) == 3 {
					break
				}
			}
		}
	}
	if len(ids) == 0 {
		return fmt.Errorf("No IDs found within 8-sitemap smoke limit; not a completed discovery")
	}
	for id := range ids {
		r, e := n.Get(ctx, dtrpg.ProductURL(id), id, dtrpg.ProductLimit, false, false)
		if e != nil {
			fmt.Printf("Product %d: %s\n", id, dtrpg.FailureOf(e).Message)
			continue
		}
		decode := time.Now()
		if _, e = dtrpg.Validate(r.Body, id); e != nil {
			return e
		}
		fmt.Printf("Product %d: matched, %d bytes, HTTP %s, decode %s\n", id, len(r.Body), r.Duration.Round(time.Millisecond), time.Since(decode))
	}
	fmt.Printf("Bounded smoke finished: %d sitemaps, %d IDs; not a full crawl\n", len(seen), len(ids))
	return nil
}
