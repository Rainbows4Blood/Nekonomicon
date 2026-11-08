package enrichdata

import (
	"encoding/json"
	"errors"
	"net/url"
)

const Version = 1
const Model = "gpt-6.1-terra"

type Range struct {
	Min      *int     `json:"min"`
	Max      *int     `json:"max"`
	Evidence string   `json:"evidence"`
	Sources  []string `json:"sources"`
}
type Result struct {
	Players   Range     `json:"players"`
	Levels    Range     `json:"levels"`
	Settings  Settings  `json:"settings"`
	Locations Locations `json:"locations"`
	Dates     Dates     `json:"dates"`
}

func (r Range) Complete() bool { return r.Min != nil && r.Max != nil }
func (r Result) Complete() bool {
	return r.Players.Complete() && r.Levels.Complete() && r.Settings.Known() && r.Locations.Known() && r.Dates.Known()
}
func (r Result) Validate() error {
	for i, v := range []Range{r.Players, r.Levels} {
		minimum := 0
		if i == 0 {
			minimum = 1
		}
		if v.Min != nil && (*v.Min < minimum || *v.Min > 10000) || v.Max != nil && (*v.Max < minimum || *v.Max > 10000) || v.Min != nil && v.Max != nil && *v.Min > *v.Max {
			return errors.New("Model returned an invalid range")
		}
		if len(v.Evidence) > 8000 || len(v.Sources) > 12 || ((v.Min != nil || v.Max != nil) && v.Evidence == "") {
			return errors.New("Model returned unsupported values or oversized evidence")
		}
		for _, s := range v.Sources {
			u, err := url.Parse(s)
			if err != nil || len(s) > 2048 || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
				return errors.New("Model returned an invalid evidence URL")
			}
		}
	}
	return r.validateContext()
}
func Merge(old, next Range) Range {
	changed := false
	if old.Min == nil && next.Min != nil {
		old.Min = next.Min
		changed = true
	}
	if old.Max == nil && next.Max != nil {
		old.Max = next.Max
		changed = true
	}
	if changed || old.Evidence == "" {
		if old.Evidence != "" && next.Evidence != "" && old.Evidence != next.Evidence {
			old.Evidence += "\n" + next.Evidence
		} else if next.Evidence != "" {
			old.Evidence = next.Evidence
		}
		for _, u := range next.Sources {
			found := false
			for _, v := range old.Sources {
				found = found || u == v
			}
			if !found {
				old.Sources = append(old.Sources, u)
			}
		}
	}
	return old
}
func Schema() map[string]any {
	r := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"min", "max", "evidence", "sources"}, "properties": map[string]any{"min": map[string]any{"type": []string{"integer", "null"}}, "max": map[string]any{"type": []string{"integer", "null"}}, "evidence": map[string]any{"type": "string"}, "sources": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}}
	return object(map[string]any{"players": r, "levels": r, "settings": settingsSchema(), "locations": locationsSchema(), "dates": datesSchema()})
}
func (r Result) JSON() string { b, _ := json.Marshal(r); return string(b) }
