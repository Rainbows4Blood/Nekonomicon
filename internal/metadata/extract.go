package metadata

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Projection struct {
	Values   []any
	Rows     map[string][][]any
	Warnings []string
	Failure  string
	Hash     string
	rowCount int
}

var decimal = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)
var integer = regexp.MustCompile(`^[0-9]+$`)

func (p *Projection) warn(path, why string) {
	message := path + ": " + why
	for _, s := range p.Warnings {
		if s == message {
			return
		}
	}
	if len(p.Warnings) < 32 {
		p.Warnings = append(p.Warnings, message)
	}
}
func object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func (p *Projection) value(m map[string]any, path, kind string) any {
	var v any = m
	for _, key := range strings.Split(path, ".") {
		if v == nil {
			return nil
		}
		o, ok := v.(map[string]any)
		if !ok {
			p.warn(path, "expected an object")
			return nil
		}
		v = o[key]
	}
	if v == nil {
		return nil
	}
	switch kind {
	case "text":
		if s, ok := v.(string); ok {
			return s
		}
	case "bool":
		if b, ok := v.(bool); ok {
			return b
		}
	case "int":
		if n, ok := v.(json.Number); ok {
			if i, e := n.Int64(); e == nil {
				return i
			}
		}
	case "decimal":
		if n, ok := v.(json.Number); ok && decimal.MatchString(string(n)) {
			return string(n)
		}
	case "integerText", "decimalText":
		if s, ok := v.(string); ok {
			s = strings.TrimSpace(s)
			if s == "" {
				return nil
			}
			if kind == "decimalText" && decimal.MatchString(s) {
				return s
			}
			if kind == "integerText" && integer.MatchString(s) {
				if n, e := strconv.ParseInt(s, 10, 64); e == nil {
					return n
				}
			}
		}
	}
	p.warn(path, "value could not be safely converted; original retained in response")
	return nil
}
func (p *Projection) row(t Table, m map[string]any) []any {
	r := make([]any, len(t.Fields))
	for i, f := range t.Fields {
		r[i] = p.value(m, f.Path, f.Kind)
	}
	return r
}
func (p *Projection) add(t Table, m map[string]any) {
	p.rowCount++
	if p.rowCount > MaxRows {
		p.Failure = "Response contains too many metadata entries"
		return
	}
	p.Rows[t.Name] = append(p.Rows[t.Name], p.row(t, m))
}
func num(n int) json.Number { return json.Number(strconv.Itoa(n)) }
func (p *Projection) array(v any, path string) []any {
	if v == nil {
		return nil
	}
	a, ok := v.([]any)
	if !ok {
		p.warn(path, "expected a list")
		return nil
	}
	return a
}
func (p *Projection) item(v any, path string) map[string]any {
	m := object(v)
	if m == nil {
		p.warn(path, "expected an object")
	}
	return m
}

// Extract never mutates or reserializes the response kept by the crawler.
// Diagnostics contain fixed field names and reasons, never remote values.
func Extract(body []byte, id int64) Projection {
	p := Projection{Rows: map[string][][]any{}, Warnings: []string{}}
	h := sha256.Sum256(body)
	p.Hash = hex.EncodeToString(h[:])
	if len(body) > MaxBody || !json.Valid(body) {
		p.Failure = "Response is invalid or exceeds the metadata size limit"
		return p
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var root map[string]any
	if d.Decode(&root) != nil || root == nil {
		p.Failure = "Response is not a product object"
		return p
	}
	m := root
	if v, exists := root["data"]; exists {
		data := object(v)
		if data == nil || data["type"] != "Product" || object(data["attributes"]) == nil {
			p.Failure = "Response has an unsupported product envelope"
			return p
		}
		m = object(data["attributes"])
		// Join included resources by both type and ID, in relationship order.
		included := map[string]map[string]any{}
		for _, v := range p.array(root["included"], "included") {
			r := object(v)
			if r == nil {
				continue
			}
			t, ok1 := r["type"].(string)
			key, ok2 := r["id"].(string)
			if ok1 && ok2 && object(r["attributes"]) != nil {
				included[t+"\x00"+key] = object(r["attributes"])
			}
		}
		rels := object(data["relationships"])
		for _, name := range []string{"publisher", "categories", "filters"} {
			rel := object(rels[name])
			if rel == nil {
				continue
			}
			refs := rel["data"]
			resolve := func(v any) any {
				r := object(v)
				t, _ := r["type"].(string)
				key, _ := r["id"].(string)
				if a := included[t+"\x00"+key]; a != nil {
					return a
				}
				p.warn(name, "linked resource is missing from included")
				return nil
			}
			if name == "publisher" {
				if refs != nil {
					m[name] = resolve(refs)
				}
				continue
			}
			var out []any
			for _, v := range p.array(refs, name) {
				if r := resolve(v); r != nil {
					out = append(out, r)
				}
			}
			m[name] = out
		}
	}
	if n, ok := m["productId"].(json.Number); !ok || string(n) != strconv.FormatInt(id, 10) || id <= 0 {
		p.Failure = "Response product ID does not match the saved product"
		return p
	}
	p.Values = p.row(Product, m)
	if desc := object(m["description"]); desc == nil || desc["name"] == nil || desc["name"] == "" {
		p.warn("description.name", "product title is missing")
	}
	for _, role := range []string{"authors", "artists", "editors", "contributors"} {
		for i, v := range p.array(m[role], role) {
			p.add(Creators, map[string]any{"role": role, "ordinal": num(i), "name": v})
		}
	}
	for _, entry := range []struct {
		key string
		t   Table
	}{{"options", Options}, {"files", Files}} {
		for i, v := range p.array(m[entry.key], entry.key) {
			if a := p.item(v, entry.key); a != nil {
				a["ordinal"] = num(i)
				p.add(entry.t, a)
			}
		}
	}
	for _, kind := range []string{"categories", "filters", "storefrontPrimaryFilterValues"} {
		for i, v := range p.array(m[kind], kind) {
			a := p.item(v, kind)
			if a == nil {
				continue
			}
			a["kind"], a["ordinal"] = kind, num(i)
			if kind == "categories" {
				a["taxonomyId"] = a["categoryId"]
			} else {
				a["taxonomyId"] = a["filterId"]
			}
			p.add(Taxonomy, a)
			for j, v := range p.array(a["descriptions"], kind+".descriptions") {
				if label := p.item(v, kind+".descriptions"); label != nil {
					label["kind"], label["taxonomyOrdinal"], label["ordinal"] = kind, num(i), num(j)
					p.add(Labels, label)
				}
			}
			for j, v := range p.array(a["ancestors"], kind+".ancestors") {
				p.add(Ancestors, map[string]any{"kind": kind, "taxonomyOrdinal": num(i), "ordinal": num(j), "ancestorId": v})
			}
		}
	}
	for _, kind := range []string{"descHeaders", "descFooters", "integrationList"} {
		v := m[kind]
		if kind == "integrationList" && object(v) != nil {
			// Some responses encode sparse arrays as objects. Keep numeric order.
			o := object(v)
			keys := make([]int, 0, len(o))
			valid := true
			for k := range o {
				n, e := strconv.Atoi(k)
				if e != nil || n < 0 || strconv.Itoa(n) != k {
					valid = false
					break
				}
				keys = append(keys, n)
			}
			if !valid {
				p.warn(kind, "unsupported collection keys")
				continue
			}
			sort.Ints(keys)
			for _, k := range keys {
				p.add(Texts, map[string]any{"kind": kind, "ordinal": num(k), "value": o[strconv.Itoa(k)]})
			}
			continue
		}
		for i, v := range p.array(v, kind) {
			p.add(Texts, map[string]any{"kind": kind, "ordinal": num(i), "value": v})
		}
	}
	pub := object(m["publisher"])
	for i, v := range p.array(pub["descriptions"], "publisher.descriptions") {
		if a := p.item(v, "publisher.descriptions"); a != nil {
			p.add(Texts, map[string]any{"kind": "publisherURL", "ordinal": num(i), "value": a["url"]})
		}
	}
	if p.Failure != "" {
		p.Values = nil
		p.Rows = nil
	}
	return p
}

func (p Projection) Diagnostic() string {
	if p.Failure != "" {
		return p.Failure
	}
	if len(p.Warnings) > 0 {
		return fmt.Sprintf("%d metadata field warnings; original values retained", len(p.Warnings))
	}
	return ""
}
