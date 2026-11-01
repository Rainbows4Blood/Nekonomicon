// smoke performs a bounded, opt-in live check. Only sanitized counts/timings
// leave the temporary catalog; no imported secrets or responses are printed.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"github.com/Rainbows4Blood/Nekonomicon/internal/auth"
	"github.com/Rainbows4Blood/Nekonomicon/internal/dtrpg"
	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
	"os"
	"sort"
	"time"
)

func main() {
	profile := flag.String("profile", "", "Firefox profile name (required)")
	live := flag.Bool("live", false, "allow the bounded live check")
	covers := flag.Bool("covers", false, "also check at most 3 primary covers after saving products")
	flag.Parse()
	if !*live || *profile == "" {
		fmt.Fprintln(os.Stderr, "Use -live -profile NAME: at most 8 sitemaps, 1 connection product and 3 discovered products")
		os.Exit(2)
	}
	if err := run(*profile, *covers); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(name string, covers bool) error {
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
	cookies, warnings, err := auth.Import(*p, 0)
	if err != nil {
		return err
	}
	for _, w := range warnings {
		fmt.Println(w)
	}
	fmt.Printf("Imported %d scoped cookies (values withheld)\n", len(cookies.Imported))
	n := dtrpg.NewNetwork(cookies, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	start := time.Now()
	if err = n.Test(ctx, 584455); err != nil {
		return err
	}
	fmt.Printf("Website/API connection passed in %s\n", time.Since(start).Round(time.Millisecond))
	queue, err := n.Robots(ctx)
	if err != nil {
		return err
	}
	if len(queue) == 0 {
		fmt.Println("Robots declares no approved sitemaps; using the verified /sitemap.xml fallback with robots policy enforced")
		queue = []string{auth.Website + "sitemap.xml"}
	}
	seen := map[string]bool{}
	ids := map[int64]bool{}
	for len(queue) > 0 && len(seen) < 8 && len(ids) < 3 {
		u := queue[0]
		queue = queue[1:]
		if seen[u] {
			continue
		}
		seen[u] = true
		r, err := n.Get(ctx, u, 0, dtrpg.SitemapLimit, false, false)
		if err != nil {
			return err
		}
		entries, err := dtrpg.ParseSitemap(r.Body, dtrpg.SitemapLimit)
		if err != nil {
			return err
		}
		var children []string
		for _, x := range entries {
			if x.Sitemap {
				u := dtrpg.SitemapURL(x.Location)
				if u == "" {
					return fmt.Errorf("Unapproved sitemap index entry")
				}
				children = append(children, u)
			} else if id := dtrpg.ProductID(x.Location); id > 0 {
				ids[id] = true
				if len(ids) == 3 {
					break
				}
			}
		}
		queue = append(children, queue...)
		fmt.Printf("Sitemap %d: %d entries, %d child maps, %d sampled IDs\n", len(seen), len(entries), len(children), len(ids))
	}
	if len(ids) == 0 {
		return fmt.Errorf("No IDs within smoke limit; not a completed discovery")
	}
	dir, err := os.MkdirTemp("", "nekonomicon-smoke-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	s, err := store.Open(dir)
	if err != nil {
		return err
	}
	defer s.Close()
	run := store.Run{ID: "smoke", Mode: "refresh", Phase: "fetch", State: "fetching", Started: time.Now().UTC().Format(time.RFC3339Nano)}
	if err = s.CreateRun(run); err != nil {
		return err
	}
	var sorted []int64
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var images []struct {
		id  int64
		url string
	}
	for _, id := range sorted {
		r, err := n.Get(ctx, dtrpg.ProductURL(id), id, dtrpg.ProductLimit, false, false)
		if err != nil {
			return err
		}
		decode := time.Now()
		v, err := dtrpg.Validate(r.Body, id)
		decodeTime := time.Since(decode)
		if err != nil {
			return err
		}
		commit, err := s.Commit(ctx, run.ID, []store.Result{{ID: id, JSON: r.Body}})
		if err != nil {
			return fmt.Errorf("Temporary catalog commit failed")
		}
		saved, err := s.Response(id)
		if err != nil || !bytes.Equal(saved, r.Body) {
			return fmt.Errorf("Stored response did not survive unchanged")
		}
		fmt.Printf("Product %d: byte-identical, %d bytes, HTTP %s, decode %s, commit %s\n", id, len(r.Body), r.Duration.Round(time.Millisecond), decodeTime, commit.Duration)
		if v.Data.Attributes.Image != nil && *v.Data.Attributes.Image != "" {
			u, err := dtrpg.ImageURL(*v.Data.Attributes.Image)
			if err != nil {
				return err
			}
			images = append(images, struct {
				id  int64
				url string
			}{id, u})
		}
	}
	coverFailures := 0
	if covers {
		for _, im := range images {
			start := time.Now()
			path, err := n.DownloadCover(ctx, ctx, dir, im.id, im.url)
			if err != nil {
				coverFailures++
				f := dtrpg.FailureOf(err)
				fmt.Printf("Cover %d: %s (HTTP %d)\n", im.id, f.Message, f.Status)
				if _, saveErr := s.Commit(ctx, run.ID, []store.Result{{ID: im.id, Failure: &store.Failure{Phase: "cover", Kind: f.Kind, Status: f.Status, Message: f.Message}}}); saveErr != nil {
					return fmt.Errorf("Cover failure commit failed")
				}
				continue
			}
			if _, err = s.Commit(ctx, run.ID, []store.Result{{ID: im.id, Cover: &store.Cover{URL: im.url, Path: path}}}); err != nil {
				return fmt.Errorf("Cover metadata commit failed")
			}
			fmt.Printf("Cover %d: validated, atomically saved and metadata committed in %s\n", im.id, time.Since(start).Round(time.Millisecond))
		}
	}
	fmt.Printf("Bounded smoke finished: %d sitemaps, %d byte-identical products, %d cover failures; temporary catalog and covers removed on exit. Not a full crawl.\n", len(seen), len(ids), coverFailures)
	if coverFailures > 0 {
		return fmt.Errorf("Product smoke passed; cover host rejected %d images", coverFailures)
	}
	return nil
}
