package dtrpg

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/Rainbows4Blood/Nekonomicon/internal/auth"
)

type Entry struct {
	Location, Lastmod string
	Sitemap           bool
}

func SitemapURL(raw string) string {
	u, e := url.Parse(raw)
	if e != nil || !auth.Approved(u) || u.Host == "api.drivethrurpg.com" {
		return ""
	}
	p := strings.ToLower(u.Path)
	if !strings.HasSuffix(p, ".xml") && !strings.HasSuffix(p, ".xml.gz") && !strings.HasSuffix(p, "/sitemap") {
		return ""
	}
	u.Fragment = ""
	return u.String()
}
func ParseSitemap(b []byte, limit int) ([]Entry, error) {
	bad := Fail("discovery", "Incomplete or invalid sitemap XML, or decoded size limit exceeded")
	if len(b) > limit {
		return nil, bad
	}
	if bytes.HasPrefix(b, []byte{0x1f, 0x8b}) {
		var e error
		b, e = decode(b, limit)
		if e != nil {
			return nil, e
		}
	}
	dec := xml.NewDecoder(bytes.NewReader(b))
	var stack []xml.Name
	var root xml.Name
	closed := false
	var loc, last strings.Builder
	var entries []Entry
	for {
		tok, e := dec.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, bad
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if len(stack) == 0 {
				if closed || (t.Name.Local != "sitemapindex" && t.Name.Local != "urlset") {
					return nil, bad
				}
				root = t.Name
			}
			if len(stack) >= 128 {
				return nil, bad
			}
			if len(stack) == 1 {
				loc.Reset()
				last.Reset()
			}
			stack = append(stack, t.Name)
		case xml.CharData:
			if len(stack) == 0 && len(bytes.TrimSpace(t)) > 0 {
				return nil, bad
			}
			if len(stack) == 3 && stack[1].Space == root.Space && stack[2].Space == root.Space && (stack[1].Local == "url" || stack[1].Local == "sitemap") {
				switch stack[2].Local {
				case "loc":
					loc.Write(t)
				case "lastmod":
					last.Write(t)
				}
			}
		case xml.EndElement:
			if len(stack) == 2 && stack[1].Space == root.Space && ((root.Local == "urlset" && stack[1].Local == "url") || (root.Local == "sitemapindex" && stack[1].Local == "sitemap")) {
				if strings.TrimSpace(loc.String()) == "" {
					return nil, bad
				}
				entries = append(entries, Entry{strings.TrimSpace(loc.String()), strings.TrimSpace(last.String()), root.Local == "sitemapindex"})
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				closed = true
			}
		case xml.Directive:
			return nil, bad
		}
		if loc.Len() > 8192 || last.Len() > 1024 {
			return nil, bad
		}
	}
	if !closed || len(stack) > 0 {
		return nil, bad
	}
	return entries, nil
}

type Discovery struct {
	Found, Sitemaps int
	Fallback        bool
}

// Discover completes the full sitemap graph before returning IDs. Interrupted
// discovery is repeated on resume; partial results can never become a crawl.
func (n *Network) Discover(ctx context.Context, progress func(Discovery)) ([]int64, error) {
	queue, e := n.Robots(ctx)
	if e != nil {
		return nil, e
	}
	fallback := len(queue) == 0
	if fallback {
		queue = []string{auth.Website + "sitemap.xml"}
		if progress != nil {
			progress(Discovery{Fallback: true})
		}
	}
	seen := map[string]bool{}
	known := map[string]bool{}
	unique := make([]string, 0, len(queue))
	for _, u := range queue {
		if !known[u] {
			known[u] = true
			unique = append(unique, u)
		}
	}
	queue = unique
	ids := map[int64]bool{}
	processed := 0
	for len(queue) > 0 {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		raw := queue[0]
		queue = queue[1:]
		if seen[raw] {
			continue
		}
		seen[raw] = true
		if len(seen) > 100000 {
			return nil, Fail("discovery", "Sitemap graph exceeds safety limit")
		}
		r, e := n.Get(ctx, raw, 0, SitemapLimit, false, false)
		if e != nil {
			return nil, e
		}
		entries, e := ParseSitemap(r.Body, SitemapLimit)
		if e != nil {
			return nil, e
		}
		for _, x := range entries {
			if x.Sitemap {
				u := SitemapURL(x.Location)
				if u == "" {
					return nil, Fail("discovery", "Sitemap index points outside approved scope")
				}
				if !known[u] {
					known[u] = true
					if len(known) > 100000 {
						return nil, Fail("discovery", "Sitemap graph exceeds safety limit")
					}
					queue = append(queue, u)
				}
			} else if id := ProductID(x.Location); id > 0 {
				ids[id] = true
			}
		}
		processed++
		if progress != nil {
			progress(Discovery{Found: len(ids), Sitemaps: processed, Fallback: fallback})
		}
	}
	if len(ids) == 0 {
		return nil, Fail("discovery", "Sitemaps contained no product IDs")
	}
	out := make([]int64, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}
