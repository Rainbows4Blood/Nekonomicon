package auth

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
	"github.com/pierrec/lz4/v4"
	_ "modernc.org/sqlite"
)

type Profile struct {
	Name       string      `json:"name"`
	Path       string      `json:"path"`
	Containers []Container `json:"containers"`
}
type Container struct {
	ID   uint32 `json:"id"`
	Name string `json:"name"`
}

func Profiles() []Profile {
	home, _ := os.UserHomeDir()
	roots := []string{filepath.Join(home, ".mozilla", "firefox"), filepath.Join(home, "snap/firefox/common/.mozilla/firefox")}
	if runtime.GOOS == "windows" {
		roots = []string{filepath.Join(os.Getenv("APPDATA"), "Mozilla", "Firefox")}
	}
	var out []Profile
	for _, root := range roots {
		out = append(out, ProfilesAt(root)...)
	}
	return out
}
func ProfilesAt(root string) []Profile {
	b, e := os.ReadFile(filepath.Join(root, "profiles.ini"))
	if e != nil {
		return nil
	}
	var out []Profile
	var name, path string
	relative := true
	selected := false
	for _, line := range strings.Split(string(b)+"\n[end]", "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			if selected && path != "" {
				if relative {
					path = filepath.Join(root, path)
				}
				if info, e := os.Stat(path); e == nil && info.IsDir() {
					out = append(out, Profile{name, path, containers(path)})
				}
			}
			name, path = "", ""
			relative = true
			selected = strings.HasPrefix(line, "[Profile")
		} else if k, v, ok := strings.Cut(line, "="); ok && selected {
			switch k {
			case "Name":
				name = v
			case "Path":
				path = v
			case "IsRelative":
				relative = v == "1"
			}
		}
	}
	return out
}
func containers(path string) []Container {
	out := []Container{{0, "Default (no container)"}}
	b, e := os.ReadFile(filepath.Join(path, "containers.json"))
	if e != nil || len(b) > Limit {
		return out
	}
	var v struct {
		Identities []struct {
			ID   uint32 `json:"userContextId"`
			Name string `json:"name"`
		}
	}
	if json.Unmarshal(b, &v) == nil {
		for _, x := range v.Identities {
			out = append(out, Container{x.ID, x.Name})
		}
	}
	return out
}

var errImport = errors.New("Firefox cookies unavailable or unsupported; close Firefox and retry, or paste cookies manually")

func Import(profile Profile, container uint32, sources ...storefront.Source) (Cookies, []string, error) {
	source := storefront.Default(sources...)
	if !source.Valid() {
		return Cookies{}, nil, errImport
	}
	// Read-only SQLite transactions include WAL; never copy the main file alone.
	path := filepath.ToSlash(filepath.Join(profile.Path, "cookies.sqlite"))
	if runtime.GOOS == "windows" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(2000)")
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return Cookies{}, nil, errImport
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, e := db.Begin()
	if e != nil {
		return Cookies{}, nil, errImport
	}
	defer tx.Rollback()
	var schema int
	if e = tx.QueryRow("PRAGMA user_version").Scan(&schema); e != nil {
		return Cookies{}, nil, errImport
	}
	rows, e := tx.Query(`SELECT name,value,host,path,isSecure,expiry,originAttributes,creationTime FROM moz_cookies WHERE host IN (?,?,?,?,?,?)`, source.Domain(), "."+source.Domain(), "www."+source.Domain(), ".www."+source.Domain(), "api."+source.Domain(), ".api."+source.Domain())
	if e != nil {
		return Cookies{}, nil, errImport
	}
	out := Cookies{}
	warn := map[string]bool{}
	total := 0
	identities := map[string]bool{}
	add := func(c Cookie, ctx string) error {
		id, site, err := Context(ctx, source)
		if err != nil {
			warn[err.Error()] = true
			return nil
		}
		if id != container {
			return nil
		}
		c.FirstParty = site
		c.HostOnly = !strings.HasPrefix(c.Domain, ".")
		if c.Path == "" {
			c.Path = "/"
		}
		if !strings.HasPrefix(c.Path, "/") || strings.Contains(c.Value, ";") {
			warn["Malformed cookie omitted"] = true
			return nil
		}
		if _, err = Paste(c.Name + "=" + c.Value); err != nil {
			warn["Malformed cookie omitted"] = true
			return nil
		}
		if schema >= 16 && c.Expires > 0 {
			c.Expires /= 1000
		}
		key := c.Name + "\x00" + c.Domain + "\x00" + c.Path + "\x00" + c.FirstParty
		if identities[key] {
			return nil
		}
		identities[key] = true
		total += len(c.Name) + len(c.Value) + len(c.Domain) + len(c.Path)
		if total > Limit {
			return errImport
		}
		out.Imported = append(out.Imported, c)
		return nil
	}
	for rows.Next() {
		var c Cookie
		var ctx string
		if rows.Scan(&c.Name, &c.Value, &c.Domain, &c.Path, &c.Secure, &c.Expires, &ctx, &c.Created) != nil {
			rows.Close()
			return Cookies{}, nil, errImport
		}
		if add(c, ctx) != nil {
			rows.Close()
			return Cookies{}, nil, errImport
		}
	}
	e = rows.Err()
	rows.Close()
	tx.Rollback()
	if e != nil {
		return Cookies{}, nil, errImport
	}
	entries, e := readSession(profile.Path)
	if e != nil {
		warn["Current Firefox session snapshot unavailable; database cookies loaded. Manual paste can include unsaved session cookies."] = true
	}
	for _, s := range entries {
		if !source.Relevant(s.Host) {
			continue
		}
		ctx := url.Values{}
		valid := true
		for k, v := range s.Attrs {
			if k == "geckoViewSessionContextId" && string(v) == `""` {
				continue
			}
			switch k {
			case "userContextId", "privateBrowsingId":
				var n uint32
				if json.Unmarshal(v, &n) != nil {
					valid = false
				} else {
					ctx.Set(k, fmt.Sprint(n))
				}
			case "partitionKey", "firstPartyDomain":
				var text string
				if json.Unmarshal(v, &text) != nil {
					valid = false
				} else {
					ctx.Set(k, text)
				}
			default:
				valid = false
			}
		}
		if !valid {
			warn["Unsupported Firefox session context omitted"] = true
			continue
		}
		c := Cookie{Name: s.Name, Value: s.Value, Domain: s.Host, Path: s.Path, Secure: s.Secure, Expires: s.Expiry}
		if add(c, "^"+ctx.Encode()) != nil {
			return Cookies{}, nil, errImport
		}
	}
	var warnings []string
	for w := range warn {
		warnings = append(warnings, w)
	}
	if len(out.Imported) == 0 {
		return out, warnings, fmt.Errorf("No %s cookies in this Firefox profile/container; sign in there or paste cookies", source.Name())
	}
	return out, warnings, nil
}

type sessionCookie struct {
	Host, Name, Value, Path string
	Secure                  bool
	Expiry                  int64
	Attrs                   map[string]json.RawMessage `json:"originAttributes"`
}

func readSession(path string) ([]sessionCookie, error) {
	var selected string
	var newest time.Time
	for _, p := range []string{"sessionstore-backups/recovery.jsonlz4", "sessionstore.jsonlz4"} {
		p = filepath.Join(path, p)
		info, e := os.Stat(p)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return nil, errImport
		}
		if info.ModTime().After(newest) {
			selected = p
			newest = info.ModTime()
		}
	}
	if selected == "" {
		return nil, nil
	}
	f, e := os.Open(selected)
	if e != nil {
		return nil, errImport
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, (32<<20)+1))
	if e != nil || len(b) > 32<<20 || len(b) < 12 || string(b[:8]) != "mozLz40\x00" {
		return nil, errImport
	}
	defer clear(b)
	n := binary.LittleEndian.Uint32(b[8:12])
	if n > 64<<20 {
		return nil, errImport
	}
	decoded := make([]byte, n)
	defer clear(decoded)
	actual, e := lz4.UncompressBlock(b[12:], decoded)
	if e != nil || actual != int(n) {
		return nil, errImport
	}
	var v struct{ Cookies []sessionCookie }
	if json.Unmarshal(decoded, &v) != nil {
		return nil, errImport
	}
	return v.Cookies, nil
}
