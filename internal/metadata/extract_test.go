package metadata

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func field(p Projection, name string) any {
	for i, f := range Product.Fields {
		if f.Column == name {
			return p.Values[i]
		}
	}
	return nil
}
func TestConservativeValuesCollectionsAndRedactedWarnings(t *testing.T) {
	body := []byte(`{"productId":42,"description":{"name":"A book","description":"<p>Unchanged</p>"},"price":12.34567890123456789,"rating":"4.0","pagecount":"iv + 24","isPwyw":false,"isbn":"000123","authors":["A","B"],"integrationList":{"5":"second","0":"first"},"files":[{"index":2,"filename":"one.pdf","size":9007199254740993}],"options":[{"price":0,"addOnInfo":{"price":1.95}}],"categories":[{"categoryId":12,"ancestors":[1,2],"descriptions":[{"languageId":1,"name":"One"},{"languageId":2,"name":"Zwei"}]}]}`)
	p := Extract(body, 42)
	if p.Failure != "" {
		t.Fatal(p.Failure)
	}
	if field(p, "price") != "12.34567890123456789" || field(p, "rating_value") != "4.0" || field(p, "page_count") != nil || field(p, "page_count_text") != "iv + 24" || field(p, "isbn") != "000123" || field(p, "is_pwyw") != false {
		t.Fatal("lossy scalar conversion", p.Values)
	}
	if len(p.Warnings) != 1 || strings.Contains(p.Warnings[0], "iv +") {
		t.Fatal("missing or unsafe warning", p.Warnings)
	}
	if len(p.Rows[Creators.Name]) != 2 || len(p.Rows[Labels.Name]) != 2 || len(p.Rows[Ancestors.Name]) != 2 {
		t.Fatal("lost repeated metadata")
	}
	texts := p.Rows[Texts.Name]
	if texts[0][1] != int64(0) || texts[0][2] != "first" || texts[1][1] != int64(5) {
		t.Fatal("sparse collection reordered", texts)
	}
	if p.Rows[Files.Name][0][3] != int64(9007199254740993) {
		t.Fatal("integer precision lost")
	}
	if Extract([]byte(`{"productId":43}`), 42).Failure == "" {
		t.Fatal("mismatched ID accepted")
	}
	if Extract(append(body, []byte(` {}`)...), 42).Failure == "" {
		t.Fatal("trailing JSON accepted")
	}
}
func TestJSONAPIAndFlatProjectionMatch(t *testing.T) {
	body, err := os.ReadFile("../../docs/fixtures/dtrpg-product-584455.json")
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err = json.Unmarshal(body, &root); err != nil {
		t.Fatal(err)
	}
	var data struct {
		Attributes    map[string]json.RawMessage
		Relationships map[string]json.RawMessage
	}
	if err = json.Unmarshal(root["data"], &data); err != nil {
		t.Fatal(err)
	}
	var included []struct {
		Type       string
		Attributes json.RawMessage
	}
	if err = json.Unmarshal(root["included"], &included); err != nil {
		t.Fatal(err)
	}
	var categories, filters []json.RawMessage
	for _, r := range included {
		switch r.Type {
		case "Publisher":
			data.Attributes["publisher"] = r.Attributes
		case "Category":
			categories = append(categories, r.Attributes)
		case "Filter":
			filters = append(filters, r.Attributes)
		}
	}
	data.Attributes["categories"], _ = json.Marshal(categories)
	data.Attributes["filters"], _ = json.Marshal(filters)
	flat, _ := json.Marshal(data.Attributes)
	a, b := Extract(body, 584455), Extract(flat, 584455)
	if a.Failure != "" || b.Failure != "" {
		t.Fatal(a.Failure, b.Failure)
	}
	if !reflect.DeepEqual(a.Values, b.Values) || !reflect.DeepEqual(a.Rows, b.Rows) {
		t.Fatal("envelope shape changed projection")
	}
	if len(a.Rows[Taxonomy.Name]) == 0 || field(a, "publisher_name") == nil {
		t.Fatal("included resources not joined")
	}
}
func TestMalformedFieldDoesNotDiscardUsefulMetadata(t *testing.T) {
	p := Extract([]byte(`{"productId":1,"description":null,"authors":["Useful",{"secret":"do-not-log"}],"pagecount":"9999999999999999999999","price":"secret-price","integrationList":{"unexpected":"private"}}`), 1)
	if p.Failure != "" || len(p.Rows[Creators.Name]) != 2 || len(p.Warnings) < 4 {
		t.Fatal("partial extraction failed", p)
	}
	for _, w := range p.Warnings {
		if strings.Contains(w, "secret") || strings.Contains(w, "private") || strings.Contains(w, "999999") {
			t.Fatal("remote value in diagnostic", w)
		}
	}
	if Extract([]byte(`{"productId":1,"authors":[`+strings.Repeat(`"a",`, MaxRows)+`"b"]}`), 1).Failure == "" {
		t.Fatal("unbounded derived rows")
	}
}
