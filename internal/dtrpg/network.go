package dtrpg

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Rainbows4Blood/Nekonomicon/internal/auth"
	"github.com/temoto/robotstxt"
)

type policy struct {
	group *robotstxt.Group
	delay time.Duration
	next  time.Time
}
type Network struct {
	Client   *http.Client
	Attempts int
	mu       sync.Mutex
	cookies  auth.Cookies
	policies map[string]*policy
	cooldown time.Time
}

func NewNetwork(c auth.Cookies, transport http.RoundTripper) *Network {
	if transport == nil {
		transport = &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext, MaxIdleConns: 24, MaxIdleConnsPerHost: 12, MaxConnsPerHost: 12, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second}
	}
	return &Network{Client: &http.Client{Transport: transport, Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, Attempts: 3, cookies: c, policies: map[string]*policy{}}
}
func (n *Network) Replace(c auth.Cookies) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cookies = c
	n.policies = map[string]*policy{}
}
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func (n *Network) pace(ctx context.Context, u *url.URL, robots, cover bool) error {
	for {
		n.mu.Lock()
		now := time.Now()
		due := n.cooldown
		var p *policy
		if !cover && !robots {
			p = n.policies[u.Scheme+"://"+u.Host]
			if p == nil {
				n.mu.Unlock()
				return Fail("policy", "Robots policy not loaded")
			}
			if !p.group.Test(u.RequestURI()) {
				n.mu.Unlock()
				return Fail("policy", "Request excluded by robots policy")
			}
			if p.next.After(due) {
				due = p.next
			}
		}
		if !due.After(now) {
			if p != nil {
				p.next = now.Add(p.delay)
			}
			n.mu.Unlock()
			return ctx.Err()
		}
		n.mu.Unlock()
		if e := sleep(ctx, time.Until(due)); e != nil {
			return e
		}
	}
}

type Response struct {
	Body     []byte
	Type     string
	Status   int
	Duration time.Duration
}

func bounded(r io.Reader, limit int) ([]byte, error) {
	b, e := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if e != nil {
		return nil, Fail("transient", "Response read failed")
	}
	if len(b) > limit {
		return nil, Fail("size", "Response exceeds decoded size limit")
	}
	return b, nil
}
func decode(b []byte, limit int) ([]byte, error) {
	r, e := gzip.NewReader(bytes.NewReader(b))
	if e != nil {
		return nil, Fail("schema", "Malformed gzip response")
	}
	defer r.Close()
	return bounded(r, limit)
}
func (n *Network) Get(ctx context.Context, raw string, product int64, limit int, robots, cover bool) (Response, error) {
	return n.GetForRun(ctx, ctx, raw, product, limit, robots, cover)
}

// Starts can be cancelled to pause scheduling/retries while the current HTTP
// request settles under ctx. This keeps pause bounded without dropping successes.
func (n *Network) GetForRun(ctx, starts context.Context, raw string, product int64, limit int, robots, cover bool) (Response, error) {
	var last error
	for attempt := 0; attempt < n.Attempts; attempt++ {
		r, e := n.once(ctx, starts, raw, product, limit, robots, cover)
		if e == nil {
			return r, nil
		}
		last = e
		f := FailureOf(e)
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		if f.Kind != "transient" {
			return Response{}, e
		}
		delay := time.Duration(1<<attempt)*time.Second + time.Duration(rand.IntN(300))*time.Millisecond
		if f.RetryAfter > delay {
			delay = f.RetryAfter
		}
		if f.Status == 429 && f.RetryAfter == 0 && delay < 60*time.Second {
			delay = 60 * time.Second
		}
		if f.Status == 429 || f.RetryAfter > 0 {
			n.mu.Lock()
			until := time.Now().Add(delay)
			if until.After(n.cooldown) {
				n.cooldown = until
			}
			n.mu.Unlock()
		}
		if attempt+1 < n.Attempts {
			if e = sleep(starts, delay); e != nil {
				return Response{}, e
			}
		}
	}
	return Response{}, last
}
func (n *Network) once(ctx, starts context.Context, raw string, product int64, limit int, robots, cover bool) (Response, error) {
	original, e := url.Parse(raw)
	if e != nil {
		return Response{}, Fail("schema", "Invalid URL")
	}
	approved := func(u *url.URL) bool {
		if cover {
			return u.Scheme == "https" && u.Host == "d1vzi28wh99zvq.cloudfront.net" && u.User == nil
		}
		return auth.Approved(u)
	}
	if !approved(original) {
		return Response{}, Fail("scope", "Unapproved request origin")
	}
	u := original
	start := time.Now()
	for redirects := 0; redirects <= 8; redirects++ {
		if e = n.pace(starts, u, robots, cover); e != nil {
			return Response{}, e
		}
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if e != nil {
			return Response{}, Fail("schema", "Invalid request")
		}
		req.Header.Set("User-Agent", UserAgent)
		req.Header.Set("Accept-Encoding", "gzip")
		req.Header.Set("Accept", "*/*")
		if product > 0 {
			req.Header.Set("Accept", "application/json")
		}
		if !cover {
			n.mu.Lock()
			h := n.cookies.Header(u)
			n.mu.Unlock()
			if h != "" {
				req.Header.Set("Cookie", h)
			}
		}
		resp, e := n.Client.Do(req)
		if e != nil {
			if ctx.Err() != nil {
				return Response{}, ctx.Err()
			}
			return Response{}, Fail("transient", "Transport or response timeout")
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 && resp.StatusCode != 304 {
			next, e := u.Parse(resp.Header.Get("Location"))
			resp.Body.Close()
			if e != nil || !approved(next) || next.Scheme != original.Scheme || next.Host != original.Host || product > 0 && (next.Path != original.Path || next.Query().Encode() != original.Query().Encode()) {
				return Response{}, Fail("authentication", "Redirect left approved request scope")
			}
			u = next
			continue
		}
		body, e := bounded(resp.Body, limit)
		resp.Body.Close()
		if e != nil {
			return Response{}, e
		}
		encoding := resp.Header.Get("Content-Encoding")
		if encoding == "gzip" {
			body, e = decode(body, limit)
			if e != nil {
				return Response{}, e
			}
		} else if encoding != "" && encoding != "identity" {
			return Response{}, Fail("schema", "Unsupported content encoding")
		}
		var f *Failure
		if resp.Header.Get("cf-mitigated") == "challenge" {
			f = Fail("authentication", "Browser challenge required; reimport cookies and test connection")
			f.Status = resp.StatusCode
		} else {
			f = classify(resp.StatusCode, body, product > 0)
		}
		if f != nil {
			if cover && f.Kind == "authentication" {
				f.Kind = "cover"
				f.Message = "Cover host denied access or requires a browser challenge"
			}
			if s := resp.Header.Get("Retry-After"); s != "" {
				seconds, err := strconv.ParseInt(s, 10, 64)
				if err == nil && seconds > 0 {
					f.RetryAfter = time.Duration(min(seconds, 31536000)) * time.Second
				} else if when, err := http.ParseTime(s); err == nil {
					f.RetryAfter = min(max(time.Until(when), 0), 365*24*time.Hour)
				}
			}
			return Response{}, f
		}
		return Response{body, resp.Header.Get("Content-Type"), resp.StatusCode, time.Since(start)}, nil
	}
	return Response{}, Fail("schema", "Redirect limit exceeded")
}
func (n *Network) Robots(ctx context.Context) ([]string, error) {
	n.mu.Lock()
	n.policies = map[string]*policy{}
	n.mu.Unlock()
	var maps []string
	for _, origin := range []string{auth.Website, auth.API} {
		r, e := n.Get(ctx, origin+"robots.txt", 0, 1<<20, true, false)
		if e != nil && FailureOf(e).Kind != "missing" {
			return nil, e
		}
		body := strings.TrimSpace(string(r.Body))
		if strings.HasPrefix(body, "<") || strings.HasPrefix(body, "{") || strings.HasPrefix(body, "[") {
			return nil, Fail("policy", "Robots response is not a policy")
		}
		data, e := robotstxt.FromBytes([]byte(body))
		if e != nil {
			return nil, Fail("policy", "Invalid robots policy")
		}
		group := data.FindGroup(RobotsAgent)
		delay := max(group.CrawlDelay, requestRate(body))
		n.mu.Lock()
		n.policies[strings.TrimSuffix(origin, "/")] = &policy{group: group, delay: delay}
		n.mu.Unlock()
		for _, line := range strings.Split(body, "\n") {
			k, v, ok := strings.Cut(strings.Split(line, "#")[0], ":")
			if ok && strings.EqualFold(strings.TrimSpace(k), "sitemap") {
				if u := SitemapURL(strings.TrimSpace(v)); u != "" {
					maps = append(maps, u)
				}
			}
		}
	}
	return maps, nil
}

// request-rate is an extension absent from the robots parser. Select the same
// most-specific agent groups and take their strictest pacing declaration.
func requestRate(body string) time.Duration {
	type group struct {
		agents []string
		rules  []string
	}
	var groups []group
	g := group{}
	for _, line := range strings.Split(body+"\nUser-agent: __end__", "\n") {
		k, v, ok := strings.Cut(strings.Split(line, "#")[0], ":")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		if k == "user-agent" {
			if len(g.rules) > 0 {
				groups = append(groups, g)
				g = group{}
			}
			g.agents = append(g.agents, strings.ToLower(v))
		} else if len(g.agents) > 0 {
			g.rules = append(g.rules, k+":"+v)
		}
	}
	best := -1
	delay := time.Duration(0)
	for _, g := range groups {
		score := -1
		for _, a := range g.agents {
			if a == "*" {
				score = max(score, 0)
			} else if strings.Contains(strings.ToLower(RobotsAgent), a) {
				score = max(score, len(a))
			}
		}
		if score < 0 || score < best {
			continue
		}
		if score > best {
			best = score
			delay = 0
		}
		for _, rule := range g.rules {
			k, v, _ := strings.Cut(rule, ":")
			if k != "request-rate" {
				continue
			}
			count, secs, ok := strings.Cut(v, "/")
			if !ok {
				continue
			}
			n, e1 := strconv.ParseFloat(count, 64)
			s, e2 := strconv.ParseFloat(secs, 64)
			if e1 == nil && e2 == nil && n > 0 && s/n >= 0 && s/n <= 31536000 {
				delay = max(delay, time.Duration(s/n*float64(time.Second)))
			}
		}
	}
	return delay
}
func (n *Network) Test(ctx context.Context, id int64) error {
	if _, e := n.Robots(ctx); e != nil {
		return e
	}
	if _, e := n.Get(ctx, auth.Website, 0, ProductLimit, false, false); e != nil {
		return e
	}
	r, e := n.Get(ctx, ProductURL(id), id, ProductLimit, false, false)
	if e != nil {
		return e
	}
	_, e = Validate(r.Body, id)
	return e
}
