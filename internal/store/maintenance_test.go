package store

import (
	"context"
	"testing"
)

func TestDatabaseMaintenanceClaimRollsBackBothStorefronts(t *testing.T) {
	for _, action := range []string{"metadata", "groups", "catalog"} {
		table := "metadata_jobs"
		if action == "groups" {
			table = "group_jobs"
		}
		if action == "catalog" {
			table = "catalog_jobs"
		}
		t.Run(table, func(t *testing.T) {
			s := openTest(t)
			ctx := context.Background()
			if _, err := s.db.Exec(`CREATE TRIGGER fail_claim BEFORE INSERT ON ` + table + ` WHEN NEW.source='dmsguild' BEGIN SELECT RAISE(ABORT,'synthetic'); END`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.BeginDatabaseMaintenance(ctx, action); err == nil {
				t.Fatal("partial database claim succeeded")
			}
			jobs, err := s.MaintenanceJobs(ctx, action)
			if err != nil || len(jobs) != 0 {
				t.Fatal("first storefront escaped claim rollback", jobs, err)
			}
			if _, err = s.db.Exec("DROP TRIGGER fail_claim"); err != nil {
				t.Fatal(err)
			}
			jobs, err = s.BeginDatabaseMaintenance(ctx, action)
			if err != nil || len(jobs) != 2 {
				t.Fatal(jobs, err)
			}
			if _, err = s.BeginDatabaseMaintenance(ctx, map[string]string{"metadata": "groups", "groups": "catalog", "catalog": "metadata"}[action]); err == nil {
				t.Fatal("maintenance tasks overlapped")
			}
		})
	}
}
