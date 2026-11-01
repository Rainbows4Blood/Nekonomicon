package dtrpg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Rainbows4Blood/Nekonomicon/internal/auth"
	"golang.org/x/net/html"
)

const UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:158.0) Gecko/20100101 Firefox/158.0"
const RobotsAgent = "Nekonomicon"
const CDN = "https://d1vzi28wh99zvq.cloudfront.net/"
const ProductLimit = 16 << 20
const SitemapLimit = 64 << 20
const CoverLimit = 32 << 20

type Failure struct {
	Kind       string        `json:"kind"`
	Status     int           `json:"status"`
	Message    string        `json:"message"`
	RetryAfter time.Duration `json:"-"`
}

func (f *Failure) Error() string         { return f.Message }
func Fail(kind, message string) *Failure { return &Failure{Kind: kind, Message: message} }
func FailureOf(e error) *Failure {
	var f *Failure
	if errors.As(e, &f) {
		return f
	}
	return Fail("network", "Request failed or was cancelled")
}
func ProductURL(id int64) string {
	return fmt.Sprintf("%sapi/vBeta/products/%d?groupId=1&siteId=10", auth.API, id)
}
func ProductID(raw string) int64 {
	u, e := url.Parse(raw)
	if e != nil || !auth.Approved(u) || u.Host == "api.drivethrurpg.com" {
		return 0
	}
	var s string
	if u.Path == "/product_info.php" {
		vs := u.Query()["products_id"]
		if len(vs) != 1 {
			return 0
		}
		s = vs[0]
	} else {
		p := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(p) >= 2 && p[0] == "product" {
			s = p[1]
		} else if len(p) >= 3 && p[1] == "product" && len(p[0]) <= 5 && p[0] != "" {
			for _, c := range p[0] {
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-') {
					return 0
				}
			}
			s = p[2]
		}
	}
	if s == "" {
		return 0
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
	}
	id, e := strconv.ParseInt(s, 10, 64)
	if e != nil || id <= 0 {
		return 0
	}
	return id
}

type ProductData struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Attributes struct {
		ProductID int64   `json:"productId"`
		Image     *string `json:"image"`
	} `json:"attributes"`
}
type Envelope struct {
	Data ProductData `json:"data"`
}

// Presence records a field without allocating or decoding its contents.
type presence bool

func (p *presence) UnmarshalJSON(b []byte) error {
	*p = presence(!bytes.Equal(b, []byte("null")))
	return nil
}

func Validate(body []byte, id int64) (Envelope, error) {
	var v Envelope
	var wire struct {
		Data        *ProductData    `json:"data"`
		ProductID   int64           `json:"productId"`
		Image       json.RawMessage `json:"image"`
		PublisherID presence        `json:"publisherId"`
		Description presence        `json:"description"`
		Error       presence        `json:"error"`
		Errors      presence        `json:"errors"`
	}
	if json.Unmarshal(body, &wire) != nil {
		return v, Fail("schema", "Product response is not a JSON object")
	}
	if wire.Error || wire.Errors {
		return v, Fail("schema", "API returned an error envelope")
	}
	if wire.Data == nil {
		// The live vBeta endpoint also serves a flat product object. Require its
		// own ID and image field; never normalize the bytes used for storage.
		if wire.ProductID != id || id <= 0 || (wire.Image == nil && !wire.PublisherID && !wire.Description) {
			return v, Fail("schema", "Response is not a matching flat product")
		}
		v.Data.Type = "Product"
		v.Data.Attributes.ProductID = wire.ProductID
		if wire.Image != nil && json.Unmarshal(wire.Image, &v.Data.Attributes.Image) != nil {
			return v, Fail("schema", "Invalid primary image field")
		}
		return v, nil
	}
	v.Data = *wire.Data
	if v.Data.Type != "Product" {
		return v, Fail("schema", "Response resource type is not Product")
	}
	if v.Data.Attributes.ProductID != id || id <= 0 {
		return v, Fail("schema", "Response productId does not match the requested ID")
	}
	if v.Data.ID != "" && v.Data.ID != fmt.Sprint(id) && v.Data.ID != fmt.Sprintf("/api/vBeta/products/%d", id) {
		return v, Fail("schema", "Product resource ID does not match")
	}
	return v, nil
}
func labelKind(s string, productForbidden bool) string {
	s = strings.Trim(strings.ToLower(strings.Join(strings.Fields(s), " ")), " .!…:;")
	if s == "access denied" && productForbidden {
		return ""
	}
	switch s {
	case "just a moment", "attention required! | cloudflare", "verify you are human", "verifying you are human", "checking your browser", "captcha challenge", "authentication required", "login required", "sign in required", "access denied", "session expired", "your session has expired", "invalid session":
		return "authentication"
	case "age verification required", "content restricted", "product restricted", "adult content is disabled":
		if !productForbidden {
			return "restricted"
		}
	}
	return ""
}
func bodyGate(b []byte, forbidden bool) string {
	source := bytes.TrimSpace(b)
	if !bytes.HasPrefix(source, []byte("<")) {
		if len(source) <= 512 {
			return labelKind(string(source), forbidden)
		}
		return ""
	}
	if len(source) > 65536 {
		source = source[:65536]
	}
	doc, e := html.Parse(bytes.NewReader(source))
	if e != nil {
		return ""
	}
	type entry struct {
		node    *html.Node
		heading bool
	}
	stack := []entry{{doc, false}}
	var notice strings.Builder
	tooLong := len(b) > 65536
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := x.node
		if n.Type == html.ElementNode {
			excluded := n.Data == "script" || n.Data == "style" || n.Data == "template" || n.Data == "noscript"
			attrs := map[string]string{}
			for _, a := range n.Attr {
				attrs[a.Key] = a.Val
				if a.Key == "hidden" || a.Key == "inert" || a.Key == "aria-hidden" && a.Val == "true" {
					excluded = true
				}
				if a.Key == "style" {
					for _, rule := range strings.Split(a.Val, ";") {
						k, v, _ := strings.Cut(rule, ":")
						v = strings.TrimSpace(strings.Split(v, "!")[0])
						if strings.TrimSpace(k) == "display" && v == "none" || strings.TrimSpace(k) == "visibility" && v == "hidden" {
							excluded = true
						}
					}
				}
			}
			if excluded {
				continue
			}
			if n.Data == "form" && strings.HasPrefix(attrs["action"], "/cdn-cgi/challenge-platform/") && (attrs["id"] == "challenge-form" || strings.HasPrefix(attrs["id"], "cf-chl-")) {
				return "authentication"
			}
			if n.Data == "title" || n.Data == "h1" || n.Data == "h2" {
				var text strings.Builder
				nodes := []*html.Node{n}
				for len(nodes) > 0 {
					a := nodes[len(nodes)-1]
					nodes = nodes[:len(nodes)-1]
					if a.Type == html.TextNode {
						text.WriteString(a.Data)
					}
					for ch := a.LastChild; ch != nil; ch = ch.PrevSibling {
						nodes = append(nodes, ch)
					}
				}
				if kind := labelKind(text.String(), forbidden); kind != "" {
					return kind
				}
			}
		}
		if n.Type == html.TextNode && !tooLong {
			if notice.Len()+len(n.Data) > 512 {
				tooLong = true
			} else {
				notice.WriteString(n.Data)
				notice.WriteByte(' ')
			}
		}
		for ch := n.LastChild; ch != nil; ch = ch.PrevSibling {
			stack = append(stack, entry{ch, false})
		}
	}
	if !tooLong {
		return labelKind(notice.String(), forbidden)
	}
	return ""
}
func classify(status int, b []byte, product bool) *Failure {
	fail := func(k, m string) *Failure { return &Failure{Kind: k, Status: status, Message: m} }
	var flags map[string]json.RawMessage
	_ = json.Unmarshal(b, &flags)
	truth := func(keys ...string) bool {
		for _, k := range keys {
			if string(flags[k]) == "true" {
				return true
			}
		}
		return false
	}
	if truth("loginRequired", "authenticationRequired", "sessionExpired", "invalidSession") {
		return fail("authentication", "Session expired or authentication required; reimport cookies and test connection")
	}
	if truth("ageGate", "ageGateRequired", "requiresAgeVerification", "adultContentRestricted") {
		return fail("restricted", "Individual product restriction")
	}
	if status == 403 && product {
		for _, k := range []string{"message", "error", "detail"} {
			var msg string
			if json.Unmarshal(flags[k], &msg) == nil && bodyGate([]byte(msg), true) == "authentication" {
				return fail("authentication", "Explicit API session failure")
			}
		}
		if bodyGate(b, true) == "authentication" {
			return fail("authentication", "Authentication or browser challenge required")
		}
		return fail("restricted", "Product unavailable or restricted (HTTP 403)")
	}
	if truth("accessDenied") {
		return fail("authentication", "Explicit authentication denial")
	}
	if status == 401 {
		return fail("authentication", "HTTP authentication denial; reimport cookies and test connection")
	}
	if status >= 200 && status < 300 && len(flags) > 0 {
		return nil
	}
	if kind := bodyGate(b, false); kind != "" {
		return fail(kind, "Authentication, challenge, or restriction reported by server")
	}
	if status == 403 {
		return fail("authentication", "Website access denied; test connection")
	}
	if status == 404 || status == 410 {
		return fail("missing", "Resource missing or removed")
	}
	if status == 408 || status == 425 || status == 429 || status >= 500 {
		return fail("transient", "Temporary HTTP failure; retry limit reached")
	}
	if status < 200 || status >= 300 {
		return fail("schema", "Unexpected HTTP response")
	}
	return nil
}
