package store

import (
	"context"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
	"strings"
	"testing"
)

func TestGroupsLiveRefreshRollbackAndIndexedRead(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	r := runTest(t, s, "crawl")
	body := []byte(` {"productId":42,"publisherId":44,"filters":[{"filterId":44832}],"description":{"name":"Synthetic"}} `)
	if _, e := s.Commit(ctx, r.ID, []Result{{ID: 42, JSON: body}}); e != nil {
		t.Fatal(e)
	}
	official := true
	members, e := s.GroupMembers(ctx, "dnd-3", storefront.DTRPG, &official, "", 0)
	if e != nil || len(members) != 1 || members[0].ID != 42 {
		t.Fatal(members, e)
	}
	if _, e = s.db.Exec(`CREATE TRIGGER fail_groups BEFORE INSERT ON product_groups BEGIN SELECT RAISE(ABORT,'synthetic');END`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Commit(ctx, r.ID, []Result{{ID: 42, JSON: []byte(`{"productId":42,"filters":[{"filterId":44833}]}`)}}); e == nil {
		t.Fatal("group insert did not roll back batch")
	}
	got, _ := s.Response(42)
	if string(got) != string(body) {
		t.Fatal("raw response escaped rollback")
	}
	s.db.Exec(`DROP TRIGGER fail_groups`)
	// Failed refresh preserves the old good group and response; extraction failure
	// after an accepted raw response hides old membership until recovery.
	if _, e = s.Commit(ctx, r.ID, []Result{{ID: 42, Failure: &Failure{Phase: "product", Kind: "http"}}}); e != nil {
		t.Fatal(e)
	}
	members, _ = s.GroupMembers(ctx, "dnd-3", storefront.DTRPG, nil, "", 0)
	if len(members) != 1 {
		t.Fatal("failed HTTP refresh lost groups")
	}
	if _, e = s.Commit(ctx, r.ID, []Result{{ID: 42, JSON: []byte(`{"productId":99}`)}}); e != nil {
		t.Fatal(e)
	}
	members, _ = s.GroupMembers(ctx, "dnd-3", storefront.DTRPG, nil, "", 0)
	if len(members) != 0 {
		t.Fatal("stale group exposed")
	}
	if _, e = s.Commit(ctx, r.ID, []Result{{ID: 42, JSON: []byte(`{"productId":42,"filters":[{"filterId":44833}]}`)}}); e != nil {
		t.Fatal(e)
	}
	members, _ = s.GroupMembers(ctx, "dnd-3", storefront.DTRPG, nil, "", 0)
	if len(members) != 0 {
		t.Fatal("obsolete membership kept")
	}
	members, _ = s.GroupMembers(ctx, "dnd-35", storefront.DTRPG, nil, "", 0)
	if len(members) != 1 || members[0].Official != nil {
		t.Fatal("unknown official flag", members)
	}
	for _, condition := range []string{"", " AND official=1"} {
		rows, e := s.db.Query(`EXPLAIN QUERY PLAN SELECT product_id FROM current_product_groups WHERE group_id='dnd-35' AND source='dtrpg' AND product_id>0` + condition + ` ORDER BY product_id LIMIT 50`)
		if e != nil {
			t.Fatal(e)
		}
		indexed := false
		for rows.Next() {
			var a, b, c int
			var detail string
			rows.Scan(&a, &b, &c, &detail)
			if strings.Contains(detail, "SCAN p") || strings.Contains(detail, "SCAN g") {
				t.Fatal("catalog/group scan", detail)
			}
			if strings.Contains(detail, "SEARCH g USING") {
				indexed = true
			}
		}
		rows.Close()
		if !indexed {
			t.Fatal("no indexed group access")
		}
	}
}
func TestGroupsMaintenanceRecoveryAndIndependentOwnership(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	r := runTest(t, s, "seed")
	for _, id := range []int{1, 2, 3} {
		if _, e := s.db.Exec(`INSERT INTO products VALUES('dtrpg',?,'original','seed',json_object('productId',?,'filters',json_array(json_object('filterId',44705))))`, id, id); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.BeginGroups(ctx, storefront.DTRPG); e == nil {
		t.Fatal("grouping overlapped crawl")
	}
	s.UpdateRun(r.ID, "fetch", "completed", true)
	j, e := s.BeginGroups(ctx, storefront.DTRPG)
	if e != nil || j.Total != 3 {
		t.Fatal(j, e)
	}
	if _, e = s.BeginMetadata(ctx, storefront.DTRPG); e == nil {
		t.Fatal("metadata overlapped grouping")
	}
	if _, e = s.BeginGroups(ctx, storefront.DMSGuild); e == nil {
		t.Fatal("other source overlapped grouping")
	}
	rows, e := s.GroupsPage(ctx, storefront.DTRPG, 0)
	if e != nil {
		t.Fatal(e)
	}
	j, e = s.CommitGroups(ctx, j, rows[:1])
	if e != nil {
		t.Fatal(e)
	}
	s.db.Exec(`CREATE TRIGGER fail_groups BEFORE INSERT ON product_groups WHEN NEW.product_id=3 BEGIN SELECT RAISE(ABORT,'synthetic');END`)
	if _, e = s.CommitGroups(ctx, j, rows[1:]); e == nil {
		t.Fatal("partial group commit accepted")
	}
	saved, _ := s.GroupsJob(ctx, storefront.DTRPG)
	if saved.LastID != 1 || saved.Processed != 1 {
		t.Fatal("cursor escaped rollback")
	}
	members, _ := s.GroupMembers(ctx, "dnd-35", storefront.DTRPG, nil, "", 0)
	if len(members) != 1 {
		t.Fatal("partial membership escaped rollback")
	}
	s.db.Exec(`DROP TRIGGER fail_groups`)
	s.SetGroupsState(ctx, storefront.DTRPG, "paused")
	dir := s.Dir
	s.Close()
	s, e = InspectContext(ctx, dir, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	j, e = s.BeginGroups(ctx, storefront.DTRPG)
	if e != nil || j.LastID != 1 {
		t.Fatal(j, e)
	}
	rows, e = s.GroupsPage(ctx, storefront.DTRPG, j.LastID)
	if e != nil {
		t.Fatal(e)
	}
	j, e = s.CommitGroups(ctx, j, rows)
	if e != nil || j.Processed != 3 {
		t.Fatal(j, e)
	}
	s.SetGroupsState(ctx, storefront.DTRPG, "completed")
	summary, _ := s.GroupsSummary(ctx, storefront.DTRPG)
	if summary.Pending != 0 {
		t.Fatal(summary)
	}
	if _, e = s.BeginMetadata(ctx, storefront.DTRPG); e != nil {
		t.Fatal(e)
	}
	if _, e = s.BeginGroups(ctx, storefront.DTRPG); e == nil {
		t.Fatal("grouping overlapped metadata")
	}
	s.SetMetadataState(ctx, storefront.DTRPG, "ended")
	// Version changes and response changes re-enter the explicit maintenance pass.
	s.db.Exec(`UPDATE group_state SET version=0 WHERE product_id=1; UPDATE products SET fetched_at='newer' WHERE product_id=2`)
	summary, _ = s.GroupsSummary(ctx, storefront.DTRPG)
	if summary.Pending != 2 {
		t.Fatal("outdated groups not pending", summary)
	}
}
func TestGroupsSchemaV3InspectionAndCancellation(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	dir := s.Dir
	s.db.Exec(`DROP VIEW current_product_groups;DROP TABLE product_groups;DROP TABLE group_state;DROP TABLE group_jobs;DROP TABLE catalog_groups;PRAGMA user_version=3`)
	s.Close()
	s, e := InspectContext(ctx, dir, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if s.Version() != 3 {
		t.Fatal("inspection upgraded")
	}
	c, cancel := context.WithCancel(ctx)
	defer cancel()
	if e = s.Upgrade(c, func(msg string) {
		if msg == "Preparing D&D group tables…" {
			cancel()
		}
	}); e == nil {
		t.Fatal("cancelled upgrade committed")
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='catalog_groups'`).Scan(&n)
	if n != 0 || s.Version() != 3 {
		t.Fatal("partial schema escaped")
	}
	if e = s.Upgrade(ctx, nil); e != nil {
		t.Fatal(e)
	}
	s.db.QueryRow(`SELECT count(*) FROM catalog_groups`).Scan(&n)
	if n != 9 {
		t.Fatal(n)
	}
}
func TestGroupsBothStoresAndKeysetPages(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	for _, source := range []storefront.Source{storefront.DTRPG, storefront.DMSGuild} {
		id := string(source)
		if e := s.CreateRun(Run{ID: id, Mode: "new", Source: source, Phase: "fetch", State: "running", Started: "now"}); e != nil {
			t.Fatal(e)
		}
		body := []byte(`{"productId":1,"publisherId":44,"filters":[{"filterId":44833}]}`)
		if _, e := s.Commit(ctx, id, []Result{{ID: 1, JSON: body}}); e != nil {
			t.Fatal(e)
		}
		s.UpdateRun(id, "fetch", "completed", true)
	}
	rows, e := s.GroupMembers(ctx, "dnd-35", "", nil, "", 0)
	if e != nil || len(rows) != 2 || rows[0].Source != storefront.DMSGuild || rows[1].Source != storefront.DTRPG {
		t.Fatal(rows, e)
	}
	next, e := s.GroupMembers(ctx, "dnd-35", "", nil, rows[0].Source, rows[0].ID)
	if e != nil || len(next) != 1 || next[0].Source != storefront.DTRPG {
		t.Fatal(next, e)
	}
	official := true
	rows, e = s.GroupMembers(ctx, "dnd-35", storefront.DMSGuild, &official, "", 0)
	if e != nil || len(rows) != 1 || rows[0].OfficialReason != "publisher_44" {
		t.Fatal(rows, e)
	}
}
