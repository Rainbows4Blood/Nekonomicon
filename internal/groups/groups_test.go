package groups

import (
	"encoding/json"
	"github.com/Rainbows4Blood/Nekonomicon/internal/metadata"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
	"reflect"
	"sort"
	"testing"
)

func fixture(t *testing.T, source storefront.Source, fields map[string]any, ids ...int) Result {
	t.Helper()
	fields["productId"] = 1
	fields["description"] = map[string]any{"name": "Synthetic book"}
	filters := []any{}
	for _, id := range ids {
		filters = append(filters, map[string]any{"filterId": id})
	}
	fields["filters"] = filters
	b, e := json.Marshal(fields)
	if e != nil {
		t.Fatal(e)
	}
	return Derive(source, metadata.Extract(b, 1))
}
func TestEditionsBothSourcesAndAncestorPrecedence(t *testing.T) {
	cases := []struct {
		ids  []int
		want []string
	}{{[]int{44698}, []string{"dnd-0"}}, {[]int{44699, 44828}, []string{"dnd-bx"}}, {[]int{44700}, []string{"adnd-1"}}, {[]int{44701}, []string{"adnd-2"}}, {[]int{44702, 44704}, []string{"dnd-3"}}, {[]int{45348, 45351}, []string{"dnd-35"}}, {[]int{44834}, []string{"dnd-4"}}, {[]int{1000261, 45462}, []string{"dnd-5"}}, {[]int{1000259, 1000261, 1000260, 1000263}, []string{"dnd-55"}}, {[]int{1000261, 45462, 1000263}, []string{"dnd-5", "dnd-55"}}, {[]int{1120, 44702, 45348}, nil}, {[]int{44828}, nil}, {[]int{1000191}, nil}, {[]int{1000261}, nil}, {[]int{45326}, nil}}
	for _, source := range []storefront.Source{storefront.DTRPG, storefront.DMSGuild} {
		for _, tc := range cases {
			r := fixture(t, source, map[string]any{}, tc.ids...)
			var got []string
			for _, m := range r.Matches {
				got = append(got, m.Group)
			}
			sort.Strings(got)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("%s %v: got %v want %v", source, tc.ids, got, tc.want)
			}
		}
	}
}
func TestOfficialPrecedence(t *testing.T) {
	cases := []struct {
		fields map[string]any
		ids    []int
		want   int
		reason string
	}{
		{map[string]any{"publisherId": 44}, []int{45323, 45351, 45322, 1120}, 1, "publisher_44"},
		{map[string]any{"publisherId": 8957, "isCommunityContent": true}, []int{45323, 1000261}, 0, "community_content"},
		{map[string]any{"publisherId": 44, "isCommunityContent": true}, []int{45323}, 0, "community_content"},
		{map[string]any{"publisherId": 44}, []int{45326}, 0, "compatible_ogl_gsl"},
		{map[string]any{"publisherId": 99}, []int{45323}, 0, "other_publisher"},
		{map[string]any{}, []int{45323}, -1, "publisher_unknown"},
		{map[string]any{"publisherId": 44, "publisher": map[string]any{"publisherId": 99}}, nil, -1, "publisher_conflict"},
	}
	for _, tc := range cases {
		r := fixture(t, storefront.DTRPG, tc.fields, tc.ids...)
		v := -1
		if r.Official != nil {
			v = 0
			if *r.Official {
				v = 1
			}
		}
		if v != tc.want || r.OfficialReason != tc.reason {
			t.Fatalf("%+v: %+v", tc, r)
		}
	}
}
func TestNamespacesSourceScopeAndChangedTaxonomy(t *testing.T) {
	raw := []byte(`{"productId":1,"categories":[{"categoryId":44705}],"storefrontPrimaryFilterValues":[{"filterId":44281}]}`)
	p := metadata.Extract(raw, 1)
	if len(Derive(storefront.DTRPG, p).Matches) != 1 || len(Derive(storefront.DMSGuild, p).Matches) != 0 {
		t.Fatal("taxonomy namespaces or sources leaked")
	}
	p = metadata.Extract([]byte(`{"productId":1,"filters":[{"filterId":44705,"parentId":999,"groupId":27}]}`), 1)
	r := Derive(storefront.DTRPG, p)
	if len(r.Matches) != 0 || len(r.Warnings) == 0 {
		t.Fatal("changed context silently accepted")
	}
	p = metadata.Extract([]byte(`{"productId":1,"categories":[{"categoryId":9736}]}`), 1)
	if r = Derive(storefront.DMSGuild, p); len(r.Matches) != 1 || r.Matches[0].Group != "dnd-bx" {
		t.Fatal("Basic family fallback lost")
	}
}
func TestIncludedResourcesAndFailure(t *testing.T) {
	p := metadata.Extract([]byte(`{"data":{"type":"Product","attributes":{"productId":1},"relationships":{"filters":{"data":[{"type":"Filter","id":"x"}]},"publisher":{"data":{"type":"Publisher","id":"p"}}}},"included":[{"type":"Filter","id":"x","attributes":{"filterId":44832}},{"type":"Publisher","id":"p","attributes":{"publisherId":44,"name":"Wizards of the Coast"}}]}`), 1)
	r := Derive(storefront.DMSGuild, p)
	if len(r.Matches) != 1 || r.Matches[0].Group != "dnd-3" || r.Official == nil || !*r.Official {
		t.Fatal(r)
	}
	if r = Derive(storefront.DTRPG, metadata.Extract([]byte(`{"productId":2}`), 1)); r.Failure == "" || len(r.Matches) != 0 {
		t.Fatal(r)
	}
}
