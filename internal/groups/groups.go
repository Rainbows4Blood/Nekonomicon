// Package groups defines the evidence-based D&D grouping rules shared by
// live crawling and explicit local maintenance. See docs/GROUPS.md.
package groups

import (
	"fmt"
	"github.com/Rainbows4Blood/Nekonomicon/internal/metadata"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
	"strings"
)

const Version = 1

// Bump Version whenever rules or their interpretation change.
type Definition struct{ ID, Name string }

var Definitions = []Definition{{"dnd-0", "D&D 0"}, {"dnd-bx", "B/X"}, {"adnd-1", "AD&D 1"}, {"adnd-2", "AD&D 2"}, {"dnd-3", "D&D 3"}, {"dnd-35", "D&D 3.5"}, {"dnd-4", "D&D 4"}, {"dnd-5", "D&D 5"}, {"dnd-55", "D&D 5.5"}}

type Match struct{ Group, Evidence string }
type Result struct {
	Matches        []Match
	Official       *bool
	OfficialReason string
	Warnings       []string
	Failure        string
}

func value(t metadata.Table, row []any, col string) any {
	for i, f := range t.Fields {
		if f.Column == col && i < len(row) {
			return row[i]
		}
	}
	return nil
}
func Derive(source storefront.Source, p metadata.Projection) Result {
	r := Result{Warnings: []string{}, OfficialReason: "publisher_unknown", Failure: p.Failure}
	if p.Failure != "" {
		return r
	}
	verified, ok := verifiedTaxonomy[source]
	if !ok {
		r.Failure = "Unsupported grouping storefront"
		return r
	}
	filters, categories := map[int64]bool{}, map[int64]bool{}
	invalid := map[int64]bool{}
	for _, row := range p.Rows[metadata.Taxonomy.Name] {
		id, _ := value(metadata.Taxonomy, row, "taxonomy_id").(int64)
		kind, _ := value(metadata.Taxonomy, row, "kind").(string)
		if kind == "filters" || kind == "storefrontPrimaryFilterValues" {
			expected, known := verified[taxonomyKey{kind, id}]
			if !known {
				continue
			}
			parent, hasParent := value(metadata.Taxonomy, row, "parent_id").(int64)
			group, hasGroup := value(metadata.Taxonomy, row, "group_id").(int64)
			if (hasParent && parent != expected.parent) || (hasGroup && group != expected.group) {
				invalid[id] = true
				if len(r.Warnings) < 8 {
					r.Warnings = append(r.Warnings, "Known filter has an unexpected taxonomy context")
				}
				continue
			}
			filters[id] = true
		}
		if kind == "categories" {
			categories[id] = true
		}
	}
	for id := range invalid {
		delete(filters, id)
	}
	has := func(ids ...int64) bool {
		for _, id := range ids {
			if filters[id] {
				return true
			}
		}
		return false
	}
	add := func(group string, ids ...int64) {
		for _, id := range ids {
			if filters[id] {
				r.Matches = append(r.Matches, Match{group, fmt.Sprintf("filter:%d", id)})
				return
			}
		}
	}
	add("dnd-0", 44698, 45344)
	if categories[9737] && !has(44698, 45344) {
		r.Matches = append(r.Matches, Match{"dnd-0", "category:9737"})
	}
	add("dnd-bx", 44699, 45345)
	if !has(44699, 45345) {
		for _, id := range []int64{2057, 9736} {
			if categories[id] {
				r.Matches = append(r.Matches, Match{"dnd-bx", fmt.Sprintf("category:%d", id)})
				break
			}
		}
	}
	add("adnd-1", 1310, 44700, 44829, 45346)
	add("adnd-2", 1320, 44701, 44830, 45347)
	add("dnd-3", 44704, 44832, 45332, 45350)
	add("dnd-35", 44705, 44833, 45333, 45351)
	add("dnd-4", 44281, 44703, 44834, 45349)
	add("dnd-55", 1000260, 1000263)
	// Only explicit leaves distinguish fifth-edition revisions. The parents
	// are shared with 5.5 and cannot establish 2014 compatibility on their own.
	add("dnd-5", 45189, 45462, 45190)
	if has(45326, 1000259, 1000261) && !has(45189, 45462, 45190, 1000260, 1000263) {
		r.Warnings = append(r.Warnings, "Fifth-edition family does not distinguish 5e from 5.5e")
	}

	if has(1330, 44828) && !has(44698, 45344, 44699, 45345) && !categories[9737] && !categories[2057] && !categories[9736] {
		r.Warnings = append(r.Warnings, "Basic/BECMI or OD&D does not distinguish Original from Basic D&D")
	}
	if has(1120, 44702, 45325, 45348) && !has(44704, 44832, 45332, 45350, 44705, 44833, 45333, 45351) {
		r.Warnings = append(r.Warnings, "3.x/d20 does not identify D&D 3.0 or 3.5")
	}
	if len(r.Matches) == 0 && has(44827, 1300, 45323) && len(r.Warnings) == 0 {
		r.Warnings = append(r.Warnings, "D&D taxonomy has no supported edition evidence")
	}
	for _, w := range p.Warnings {
		if strings.HasPrefix(w, "filters") || strings.HasPrefix(w, "categories") || strings.HasPrefix(w, "storefrontPrimaryFilterValues") {
			r.Warnings = append(r.Warnings, "Some taxonomy fields could not be extracted")
			break
		}
	}
	pub, _ := value(metadata.Product, p.Values, "publisher_id").(int64)
	linked, _ := value(metadata.Product, p.Values, "publisher_resource_id").(int64)
	name, _ := value(metadata.Product, p.Values, "publisher_name").(string)
	community, _ := value(metadata.Product, p.Values, "is_community_content").(bool)
	set := func(b bool, reason string) { r.Official = &b; r.OfficialReason = reason }
	switch {
	case community || pub == 8957 || linked == 8957:
		set(false, "community_content")
	case has(45326, 44281):
		set(false, "compatible_ogl_gsl")
	case pub != 0 && linked != 0 && pub != linked:
		r.OfficialReason = "publisher_conflict"
		r.Warnings = append(r.Warnings, "Publisher identifiers conflict")
	case pub == 44 || linked == 44:
		if name != "" && name != "Wizards of the Coast" {
			r.OfficialReason = "publisher_conflict"
			r.Warnings = append(r.Warnings, "Publisher identifier and name conflict")
		} else {
			set(true, "publisher_44")
		}
	case pub != 0 || linked != 0 || name != "":
		if name == "Wizards of the Coast" && pub == 0 && linked == 0 {
			set(true, "publisher_name")
		} else if name == "Wizards of the Coast" {
			r.OfficialReason = "publisher_conflict"
			r.Warnings = append(r.Warnings, "Publisher identifier and name conflict")
		} else {
			set(false, "other_publisher")
		}
	case has(45322):
		set(false, "ogl_without_official_publisher")
	}
	for _, w := range p.Warnings {
		if strings.HasPrefix(w, "isCommunityContent:") {
			r.Official = nil
			r.OfficialReason = "invalid_community_flag"
			break
		}
	}
	if r.Official == nil && len(r.Matches) > 0 {
		r.Warnings = append(r.Warnings, "Official status could not be determined from publisher and community evidence")
	}
	return r
}
