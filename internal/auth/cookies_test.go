package auth

import (
	"net/url"
	"testing"
	"time"
)

func TestCookieScope(t *testing.T) {
	c := Cookies{Imported: []Cookie{{Name: "domain", Value: "synthetic", Domain: ".drivethrurpg.com", Path: "/", Secure: true}, {Name: "host", Value: "synthetic", Domain: "www.drivethrurpg.com", HostOnly: true, Path: "/en", Secure: true}, {Name: "expired", Value: "synthetic", Domain: ".drivethrurpg.com", Path: "/", Expires: time.Now().Add(-time.Hour).Unix()}}}
	for _, x := range []struct{ url, want string }{{Website + "en/product/1", "host=synthetic; domain=synthetic"}, {Website + "english", "domain=synthetic"}, {API + "api/vBeta/products/1", "domain=synthetic"}, {"https://d1vzi28wh99zvq.cloudfront.net/a.png", ""}, {"http://www.drivethrurpg.com/", ""}, {"https://www.drivethrurpg.com.evil.invalid/", ""}} {
		u, _ := url.Parse(x.url)
		if got := c.Header(u); got != x.want {
			t.Errorf("scope %s: %q", x.url, got)
		}
	}
}
func TestPasteAndContexts(t *testing.T) {
	for _, s := range []string{"a=b\r\nX: secret", "x y=z", "bad", "a=é"} {
		if _, e := Paste(s); e == nil {
			t.Errorf("accepted malformed input")
		}
	}
	if _, e := Paste(`Cookie: session={"currency": "USD"}; token=a=b`); e != nil {
		t.Fatal(e)
	}
	for _, s := range []string{"^userContextId=1&userContextId=2", "^privateBrowsingId=1", "^partitionKey=(https,evil.invalid)", "^unknown=0", "^userContextId=4294967296"} {
		if _, _, e := Context(s); e == nil {
			t.Errorf("accepted %s", s)
		}
	}
	id, site, e := Context("^userContextId=2&partitionKey=%28https%2Cdrivethrurpg.com%29")
	if e != nil || id != 2 || site != Site {
		t.Fatal("matching container/partition rejected")
	}
	c, e := Manual("web=synthetic", "api=synthetic")
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse("https://drivethrurpg.com/")
	if c.Header(u) != "" {
		t.Fatal("manual cookies widened to apex")
	}
}
