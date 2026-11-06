// Package auth imports cookies for the selected storefront. It never exposes secret values to the UI.
package auth

import (
	"errors"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

const Website = "https://www.drivethrurpg.com/"
const API = "https://api.drivethrurpg.com/"
const Site = "https://drivethrurpg.com"
const Limit = 1 << 20

type Cookie struct {
	Name, Value, Domain, Path string
	HostOnly, Secure          bool
	Expires, Created          int64
	FirstParty                string
}
type Cookies struct {
	Imported []Cookie
	Raw      map[string]string
}

func Approved(u *url.URL) bool  { return storefront.DTRPG.Approved(u) }
func Relevant(host string) bool { return storefront.DTRPG.Relevant(host) }
func (c Cookie) matches(u *url.URL, source storefront.Source) bool {
	d := strings.TrimPrefix(c.Domain, ".")
	return (u.Hostname() == d || (!c.HostOnly && strings.HasSuffix(u.Hostname(), "."+d))) && (!c.Secure || u.Scheme == "https") && (c.Expires <= 0 || c.Expires > time.Now().Unix()) && (c.FirstParty == "" || c.FirstParty == source.Site()) && (u.Path == c.Path || (strings.HasPrefix(u.Path, c.Path) && (strings.HasSuffix(c.Path, "/") || strings.HasPrefix(strings.TrimPrefix(u.Path, c.Path), "/"))))
}
func (c Cookies) Header(u *url.URL, sources ...storefront.Source) string {
	source := storefront.Default(sources...)
	if !source.Approved(u) {
		return ""
	}
	if h := c.Raw[u.Scheme+"://"+u.Host+"/"]; h != "" {
		return h
	}
	var cs []Cookie
	for _, v := range c.Imported {
		if v.matches(u, source) {
			cs = append(cs, v)
		}
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if len(cs[i].Path) != len(cs[j].Path) {
			return len(cs[i].Path) > len(cs[j].Path)
		}
		a, b := cs[i].Created, cs[j].Created
		if a == 0 {
			a = 1<<63 - 1
		}
		if b == 0 {
			b = 1<<63 - 1
		}
		return a < b
	})
	var parts []string
	for _, v := range cs {
		parts = append(parts, v.Name+"="+v.Value)
	}
	return strings.Join(parts, "; ")
}
func Paste(s string) (string, error) {
	bad := errors.New("Cookie input must contain a single line of name=value pairs, at most 1 MiB")
	if len(s) > Limit {
		return "", bad
	}
	for _, c := range []byte(s) {
		if c < 32 || c > 126 {
			return "", bad
		}
	}
	s = strings.TrimSpace(s)
	if len(s) >= 7 && strings.EqualFold(s[:7], "cookie:") {
		s = strings.TrimSpace(s[7:])
	}
	if s == "" {
		return "", bad
	}
	for _, p := range strings.Split(s, ";") {
		n, _, ok := strings.Cut(strings.TrimSpace(p), "=")
		if !ok || n == "" {
			return "", bad
		}
		for _, c := range []byte(n) {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c))) {
				return "", bad
			}
		}
	}
	return s, nil
}
func Manual(website, api string, sources ...storefront.Source) (Cookies, error) {
	source := storefront.Default(sources...)
	if !source.Valid() {
		return Cookies{}, errors.New("Unknown storefront")
	}
	c := Cookies{Raw: map[string]string{}}
	for origin, value := range map[string]string{source.Website(): website, source.API(): api} {
		if value != "" {
			h, e := Paste(value)
			if e != nil {
				return Cookies{}, e
			}
			c.Raw[origin] = h
		}
	}
	if len(c.Raw) == 0 {
		return Cookies{}, errors.New("Paste website and/or API cookies first")
	}
	return c, nil
}

// Context rejects unknown/private/foreign partitions rather than broadening scope.
func Context(s string, sources ...storefront.Source) (uint32, string, error) {
	source := storefront.Default(sources...)
	bad := errors.New("Unsupported Firefox cookie context omitted")
	if s == "" {
		return 0, "", nil
	}
	if !strings.HasPrefix(s, "^") {
		return 0, "", bad
	}
	q, e := url.ParseQuery(s[1:])
	if e != nil {
		return 0, "", bad
	}
	var container uint32
	var site string
	for k, vs := range q {
		if len(vs) != 1 {
			return 0, "", bad
		}
		v := vs[0]
		switch k {
		case "userContextId":
			if v == "" {
				return 0, "", bad
			}
			var n uint64
			for _, r := range v {
				if r < '0' || r > '9' {
					return 0, "", bad
				}
				n = n*10 + uint64(r-'0')
				if n > 1<<32-1 {
					return 0, "", bad
				}
			}
			container = uint32(n)
		case "privateBrowsingId":
			if v != "0" {
				return 0, "", bad
			}
		case "partitionKey", "firstPartyDomain":
			if v == "" {
				continue
			}
			if v != source.Domain() && v != "(https,"+source.Domain()+")" && v != "(https,"+source.Domain()+",443)" {
				return 0, "", bad
			}
			site = source.Site()
		default:
			return 0, "", bad
		}
	}
	return container, site, nil
}
