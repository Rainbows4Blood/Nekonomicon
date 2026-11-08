// Package catalog derives the small, versioned read model used by D&D browsing.
package catalog

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Rainbows4Blood/Nekonomicon/internal/groups"
	"github.com/Rainbows4Blood/Nekonomicon/internal/metadata"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
	"golang.org/x/net/html"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// Bump when display/search mappings or the shared extractor/group rules change.
const Version = 2

type Document struct {
	Adventure      bool     `json:"adventure"`
	Name           string   `json:"name"`
	Publisher      string   `json:"publisher"`
	ReleaseDate    string   `json:"releaseDate"`
	ReleaseDay     string   `json:"releaseDay"`
	Description    string   `json:"description"`
	NameKey        string   `json:"-"`
	Editions       []string `json:"editions"`
	Official       *bool    `json:"official"`
	OfficialReason string   `json:"officialReason"`
	Warnings       []string `json:"warnings"`
	Failure        string   `json:"-"`
}

func Value(p metadata.Projection, column string) any {
	for i, f := range metadata.Product.Fields {
		if f.Column == column && i < len(p.Values) {
			return p.Values[i]
		}
	}
	return nil
}
func Text(p metadata.Projection, column string) string { v, _ := Value(p, column).(string); return v }

// PlainText never passes remote markup to the desktop renderer or search index.
// Script/style/template text and remote resource elements are discarded entirely.
func PlainText(raw string) string {
	z := html.NewTokenizer(strings.NewReader(raw))
	var b strings.Builder
	skip := 0
	for {
		t := z.Next()
		if t == html.ErrorToken {
			break
		}
		switch t {
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			tag := string(name)
			if tag == "script" || tag == "style" || tag == "template" || tag == "iframe" || tag == "object" {
				if t == html.StartTagToken {
					skip++
				} else if t == html.EndTagToken && skip > 0 {
					skip--
				}
				continue
			}
			if skip == 0 && (tag == "p" || tag == "div" || tag == "br" || tag == "li" || tag == "h1" || tag == "h2" || tag == "h3" || tag == "tr") {
				b.WriteByte('\n')
			}
		case html.TextToken:
			if skip == 0 {
				b.Write(z.Text())
			}
		}
	}
	var paragraphs []string
	for _, line := range strings.Split(b.String(), "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			paragraphs = append(paragraphs, line)
		}
	}
	return strings.Join(paragraphs, "\n")
}
func NameKey(name string) string {
	folded := cases.Fold().String(norm.NFD.String(name))
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, folded)
}
func ReleaseDay(raw string) string {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, strings.TrimSpace(raw)); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return ""
}
func Derive(source storefront.Source, id int64, p metadata.Projection) Document {
	g := groups.Derive(source, p)
	d := Document{Name: strings.TrimSpace(PlainText(Text(p, "name"))), Publisher: Text(p, "publisher_name"), ReleaseDate: Text(p, "date_available"), Official: g.Official, OfficialReason: g.OfficialReason, Editions: []string{}, Warnings: []string{}, Failure: p.Failure}
	if d.Name == "" {
		d.Name = fmt.Sprintf("Untitled product %d", id)
	}
	d.Adventure = IsAdventure(source, p)
	d.Name = Limit(d.Name, 4096)
	d.Publisher = Limit(d.Publisher, 4096)
	d.NameKey = NameKey(d.Name)
	d.Description = PlainText(Text(p, "description_html"))
	if d.Description == "" {
		d.Description = PlainText(Text(p, "short_description"))
	}
	if len(d.Description) > 1<<20 {
		d.Description = Limit(d.Description, 1<<20)
		d.Warnings = append(d.Warnings, "Description display and search limited to the first MiB; original retained")
	}
	d.ReleaseDate = Limit(d.ReleaseDate, 256)
	d.ReleaseDay = ReleaseDay(d.ReleaseDate)
	d.Warnings = append(d.Warnings, p.Warnings...)
	d.Warnings = append(d.Warnings, g.Warnings...)
	if d.ReleaseDate != "" && d.ReleaseDay == "" {
		d.Warnings = append(d.Warnings, "Release date is not a recognized calendar date; sorted with unknown dates")
	}
	if len(d.Warnings) > 32 {
		d.Warnings = d.Warnings[:32]
	}
	for _, match := range g.Matches {
		d.Editions = append(d.Editions, match.Group)
	}
	return d
}

// Limit bounds display/index strings without splitting UTF-8. Originals stay intact.
func Limit(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Only explicit source taxonomy labels confer adventure eligibility. A title or
// description mentioning adventures does not turn a sourcebook into an adventure.
func IsAdventure(source storefront.Source, p metadata.Projection) bool {
	field := func(t metadata.Table, row []any, name string) any {
		for i, f := range t.Fields {
			if f.Column == name && i < len(row) {
				return row[i]
			}
		}
		return nil
	}
	for _, row := range p.Rows[metadata.Taxonomy.Name] {
		kind, _ := field(metadata.Taxonomy, row, "kind").(string)
		id, _ := field(metadata.Taxonomy, row, "taxonomy_id").(int64)
		parent, hasParent := field(metadata.Taxonomy, row, "parent_id").(int64)
		group, hasGroup := field(metadata.Taxonomy, row, "group_id").(int64)
		if kind != "filters" {
			continue
		}
		expectedParent, expectedGroup := int64(0), int64(0)
		if id == 2110 && (source == storefront.DTRPG || source == storefront.DMSGuild) {
			expectedParent, expectedGroup = 2150, 1
		}
		if source == storefront.DMSGuild && id == 44747 {
			expectedParent, expectedGroup = 44736, 27
		}
		if source == storefront.DMSGuild && id == 45393 {
			expectedParent, expectedGroup = 45341, 29
		}
		if expectedGroup != 0 && (!hasParent || parent == expectedParent) && (!hasGroup || group == expectedGroup) {
			return true
		}
	}
	for _, row := range p.Rows[metadata.Labels.Name] {
		kind, _ := field(metadata.Labels, row, "kind").(string)
		if kind != "filters" && kind != "storefrontPrimaryFilterValues" {
			continue
		}
		for i, f := range metadata.Labels.Fields {
			if (f.Column == "name" || f.Column == "slug") && i < len(row) {
				if v, ok := row[i].(string); ok {
					v = strings.ToLower(strings.TrimSpace(v))
					if v == "adventures" || v == "adventure" || v == "campaigns, adventures & modules" {
						return true
					}
				}
			}
		}
	}
	return false
}
