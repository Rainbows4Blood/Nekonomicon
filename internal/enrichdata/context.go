package enrichdata

import (
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"
)

// Empty lists mean unknown, never agnostic. Agnostic requires positive evidence
// and may coexist with named settings when an adventure explicitly supports both.
type Settings struct {
	Names    []string `json:"names"`
	Agnostic bool     `json:"agnostic"`
	Evidence string   `json:"evidence"`
	Sources  []string `json:"sources"`
}
type Location struct {
	Name    string  `json:"name"`
	Setting *string `json:"setting"`
}
type Locations struct {
	Items    []Location `json:"items"`
	Evidence string     `json:"evidence"`
	Sources  []string   `json:"sources"`
}
type WorldDate struct {
	Date     string  `json:"date"`
	Calendar *string `json:"calendar"`
	Setting  *string `json:"setting"`
}
type Dates struct {
	Items    []WorldDate `json:"items"`
	Evidence string      `json:"evidence"`
	Sources  []string    `json:"sources"`
}

func (v Settings) Known() bool  { return len(v.Names) > 0 || v.Agnostic }
func (v Locations) Known() bool { return len(v.Items) > 0 }
func (v Dates) Known() bool     { return len(v.Items) > 0 }

func validText(v string) bool {
	v = strings.TrimSpace(v)
	return v != "" && len(v) <= 512 && !strings.EqualFold(v, "unknown")
}
func optionalText(v *string) bool { return v == nil || validText(*v) }
func validEvidence(known bool, evidence string, sources []string) bool {
	if len(evidence) > 8000 || len(sources) > 12 || known && strings.TrimSpace(evidence) == "" {
		return false
	}
	for _, source := range sources {
		u, err := url.Parse(source)
		if err != nil || len(source) > 2048 || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
			return false
		}
	}
	return true
}
func (r Result) validateContext() error {
	bad := errors.New("Model returned invalid or unsupported setting, location or in-world date information")
	if len(r.Settings.Names) > 16 || len(r.Locations.Items) > 64 || len(r.Dates.Items) > 32 {
		return bad
	}
	for _, name := range r.Settings.Names {
		if !validText(name) || strings.EqualFold(name, "agnostic") || strings.EqualFold(name, "any setting") || strings.EqualFold(name, "any system") {
			return bad
		}
	}
	for _, v := range r.Locations.Items {
		if !validText(v.Name) || !optionalText(v.Setting) {
			return bad
		}
	}
	for _, v := range r.Dates.Items {
		if !validText(v.Date) || !optionalText(v.Calendar) || !optionalText(v.Setting) {
			return bad
		}
	}
	if !validEvidence(r.Settings.Known(), r.Settings.Evidence, r.Settings.Sources) || !validEvidence(r.Locations.Known(), r.Locations.Evidence, r.Locations.Sources) || !validEvidence(r.Dates.Known(), r.Dates.Evidence, r.Dates.Sources) {
		return bad
	}
	return nil
}

func object(properties map[string]any) map[string]any {
	keys := make([]string, 0, len(properties))
	for key := range properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return map[string]any{"type": "object", "additionalProperties": false, "properties": properties, "required": keys}
}
func textSchema(nullable bool) map[string]any {
	if nullable {
		return map[string]any{"type": []string{"string", "null"}}
	}
	return map[string]any{"type": "string"}
}
func listSchema(item any) map[string]any {
	return map[string]any{"type": []string{"array", "null"}, "items": item}
}
func evidenceSchema(properties map[string]any) map[string]any {
	properties["evidence"] = textSchema(false)
	properties["sources"] = map[string]any{"type": "array", "items": textSchema(false)}
	return object(properties)
}
func settingsSchema() map[string]any {
	return evidenceSchema(map[string]any{"names": listSchema(textSchema(false)), "agnostic": map[string]any{"type": "boolean"}})
}
func locationsSchema() map[string]any {
	return evidenceSchema(map[string]any{"items": listSchema(object(map[string]any{"name": textSchema(false), "setting": textSchema(true)}))})
}
func datesSchema() map[string]any {
	return evidenceSchema(map[string]any{"items": listSchema(object(map[string]any{"date": textSchema(false), "calendar": textSchema(true), "setting": textSchema(true)}))})
}

// Require explicit fields, including explicit nulls for unknowns. A missing
// enrichment object must never be mistaken for a successfully researched unknown.
func Decode(raw []byte) (Result, error) {
	var result Result
	var root map[string]json.RawMessage
	missing := errors.New("OpenAI final answer omitted required enrichment fields; retry enrichment")
	if json.Unmarshal(raw, &root) != nil || len(root) != 5 {
		return result, missing
	}
	for key, fields := range map[string][]string{
		"players": {"min", "max", "evidence", "sources"}, "levels": {"min", "max", "evidence", "sources"},
		"settings": {"names", "agnostic", "evidence", "sources"}, "locations": {"items", "evidence", "sources"}, "dates": {"items", "evidence", "sources"},
	} {
		var value map[string]json.RawMessage
		if json.Unmarshal(root[key], &value) != nil || value == nil {
			return result, missing
		}
		for _, field := range fields {
			if len(value[field]) == 0 {
				return result, missing
			}
		}
		if key == "settings" && string(value["agnostic"]) != "true" && string(value["agnostic"]) != "false" {
			return result, missing
		}
		if key == "locations" || key == "dates" {
			var items []map[string]json.RawMessage
			if json.Unmarshal(value["items"], &items) != nil {
				return result, missing
			}
			for _, item := range items {
				required := []string{"name", "setting"}
				if key == "dates" {
					required = []string{"date", "calendar", "setting"}
				}
				for _, field := range required {
					if len(item[field]) == 0 {
						return result, missing
					}
				}
			}
		}
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(&result); err != nil {
		return Result{}, errors.New("OpenAI returned invalid structured output")
	}
	if err := result.Validate(); err != nil {
		return Result{}, err
	}
	return result, nil
}
