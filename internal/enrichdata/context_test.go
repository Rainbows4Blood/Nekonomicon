package enrichdata

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestContextUnknownAgnosticAndMultipleValues(t *testing.T) {
	setting, calendar := "Forgotten Realms", "Dalereckoning"
	for _, v := range []Result{
		{},
		{Settings: Settings{Agnostic: true, Evidence: "Usable in any campaign setting"}},
		{Settings: Settings{Names: []string{"Forgotten Realms", "Eberron"}, Agnostic: true, Evidence: "Set in these worlds; adaptable to any campaign setting"}, Locations: Locations{Items: []Location{{Name: "Waterdeep", Setting: &setting}, {Name: "Sharn"}}, Evidence: "Travel between Waterdeep and Sharn"}, Dates: Dates{Items: []WorldDate{{Date: "Winter 1492–1493 DR", Calendar: &calendar, Setting: &setting}, {Date: "Ten years after the war"}}, Evidence: "Winter 1492–1493 DR; an alternative begins ten years after the war"}},
	} {
		got, err := Decode([]byte(v.JSON()))
		if err != nil || got.JSON() != v.JSON() {
			t.Fatal(got, err)
		}
		if got.Settings.Known() != (len(v.Settings.Names) > 0 || v.Settings.Agnostic) {
			t.Fatal(got)
		}
	}
	for _, v := range []Result{
		{Settings: Settings{Agnostic: true}},
		{Settings: Settings{Names: []string{"Unknown"}, Evidence: "No setting specified"}},
		{Settings: Settings{Names: []string{"Agnostic"}, Evidence: "Any setting"}},
		{Locations: Locations{Items: []Location{{Name: "Waterdeep"}}}},
		{Dates: Dates{Items: []WorldDate{{Date: ""}}, Evidence: "Unknown"}},
		{Dates: Dates{Items: []WorldDate{{Date: "1492 DR"}}, Evidence: "1492 DR", Sources: []string{"javascript:alert(1)"}}},
	} {
		if v.Validate() == nil {
			t.Fatal("unsupported value accepted", v)
		}
	}
}

func TestContextWireMustExplicitlyIncludeUnknowns(t *testing.T) {
	valid := Result{}.JSON()
	for _, field := range []string{"players", "levels", "settings", "locations", "dates"} {
		var root map[string]json.RawMessage
		json.Unmarshal([]byte(valid), &root)
		delete(root, field)
		raw, _ := json.Marshal(root)
		if _, err := Decode(raw); err == nil {
			t.Fatal("missing field accepted", field)
		}
	}
	for _, raw := range []string{
		strings.Replace(valid, `"agnostic":false`, `"agnostic":null`, 1),
		strings.Replace(valid, `"names":null`, `"names":null,"unexpected":true`, 1),
		strings.Replace(valid, `"items":null`, `"items":[{"name":"Waterdeep"}]`, 1),
	} {
		if _, err := Decode([]byte(raw)); err == nil {
			t.Fatal("malformed unknown accepted", raw)
		}
	}
}
