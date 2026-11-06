// Package storefront defines the two supported stores, not a plugin registry.
package storefront

import (
	"fmt"
	"net/url"
	"strings"
)

type Source string

const (
	DTRPG    Source = "dtrpg"
	DMSGuild Source = "dmsguild"
)

func (s Source) Valid() bool { return s == DTRPG || s == DMSGuild }
func (s Source) Name() string {
	if s == DMSGuild {
		return "DM’s Guild"
	}
	return "DriveThruRPG"
}
func (s Source) Domain() string {
	if s == DMSGuild {
		return "dmsguild.com"
	}
	return "drivethrurpg.com"
}
func (s Source) Website() string { return "https://www." + s.Domain() + "/" }
func (s Source) API() string     { return "https://api." + s.Domain() + "/" }
func (s Source) Site() string    { return "https://" + s.Domain() }
func (s Source) ProbeID() int64 {
	if s == DMSGuild {
		return 170778
	}
	return 584455
}
func (s Source) ProductURL(id int64) string {
	u := fmt.Sprintf("%sapi/vBeta/products/%d", s.API(), id)
	// DM's Guild's host-scoped JSON:API endpoint needs no DTRPG site selector.
	if s == DTRPG {
		u += "?groupId=1&siteId=10"
	}
	return u
}
func (s Source) Approved(u *url.URL) bool {
	return s.Valid() && u != nil && u.Scheme == "https" && u.User == nil && u.Port() == "" &&
		(u.Host == s.Domain() || u.Host == "www."+s.Domain() || u.Host == "api."+s.Domain())
}
func (s Source) WebsiteURL(u *url.URL) bool { return s.Approved(u) && u.Host != "api."+s.Domain() }
func (s Source) Relevant(host string) bool {
	host = strings.TrimPrefix(host, ".")
	return s.Valid() && (host == s.Domain() || host == "www."+s.Domain() || host == "api."+s.Domain())
}

// Default preserves existing DTRPG callers and catalogs.
func Default(s ...Source) Source {
	if len(s) == 0 || s[0] == "" {
		return DTRPG
	}
	return s[0]
}
