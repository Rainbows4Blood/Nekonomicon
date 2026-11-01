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
	Skipped         int
}

// DiscoveryState retains only IDs and the sitemap frontier, never response bodies.
// One run coordinator owns it; a new run must use a fresh state.
type DiscoveryState struct {
	initialized bool
	queue       []string
	known       map[string]bool
	ids         map[int64]bool
	processed   int
	skipped     int
	fallback    bool
}

// Discover completes the permitted sitemap graph before returning IDs.
func (n *Network) Discover(ctx context.Context, progress func(Discovery)) ([]int64, error) {
	return n.DiscoverWithState(ctx, &DiscoveryState{}, progress)
}

// DiscoverWithState continues unfinished discovery in the same process. Failed
// or cancelled maps stay at the front; completed and robots-excluded maps do not.
// Robots rules are refreshed on each call, including after a connection change.
func (n *Network) DiscoverWithState(ctx context.Context, state *DiscoveryState, progress func(Discovery)) ([]int64, error) {
	report := func() {
		if progress != nil {
			progress(Discovery{Found: len(state.ids), Sitemaps: state.processed, Fallback: state.fallback, Skipped: state.skipped})
		}
	}
	report()
	roots, e := n.Robots(ctx)
	if e != nil {
		return nil, e
	}
	if !state.initialized {
		state.fallback = len(roots) == 0
		if state.fallback {
			roots = []string{auth.Website + "sitemap.xml"}
		}
		state.known = map[string]bool{}
		state.ids = map[int64]bool{}
		for _, u := range roots {
			if !state.known[u] {
				state.known[u] = true
				state.queue = append(state.queue, u)
			}
		}
		state.initialized = true
	}
	report()
	for len(state.queue) > 0 {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		if len(state.known) > 100000 {
			return nil, Fail("discovery", "Sitemap graph exceeds safety limit")
		}
		raw := state.queue[0]
		r, e := n.Get(ctx, raw, 0, SitemapLimit, false, false)
		if e != nil {
			if FailureOf(e).Kind == "robots" {
				state.queue = state.queue[1:]
				state.skipped++
				report()
				continue
			}
			return nil, e
		}
		entries, e := ParseSitemap(r.Body, SitemapLimit)
		if e != nil {
			return nil, e
		}
		// Validate all child URLs before changing the retained frontier.
		var children []string
		for _, x := range entries {
			if x.Sitemap {
				u := SitemapURL(x.Location)
				if u == "" {
					return nil, Fail("discovery", "Sitemap index points outside approved scope")
				}
				children = append(children, u)
			} else if id := ProductID(x.Location); id > 0 {
				state.ids[id] = true
			}
		}
		for _, u := range children {
			if !state.known[u] {
				if len(state.known) >= 100000 {
					return nil, Fail("discovery", "Sitemap graph exceeds safety limit")
				}
				state.known[u] = true
				state.queue = append(state.queue, u)
			}
		}
		state.queue = state.queue[1:]
		state.processed++
		report()
	}
	if len(state.ids) == 0 {
		return nil, Fail("discovery", "Permitted sitemaps contained no product IDs")
	}
	out := make([]int64, 0, len(state.ids))
	for id := range state.ids {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}
