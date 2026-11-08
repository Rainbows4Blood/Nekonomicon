package maintenance

import (
	"context"
	"testing"
	"time"

	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
)

func TestCountTimeoutDoesNotUndoCompletedIndexing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := store.InspectContext(ctx, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Upgrade(ctx, nil); err != nil {
		t.Fatal(err)
	}
	m, err := New(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	read := m.readSummary
	var previous context.Context
	m.readSummary = func(ctx context.Context, action string) (store.MetadataSummary, error) {
		if previous == ctx {
			t.Fatal("count queries share one timeout")
		}
		previous = ctx
		if action == "catalog" {
			return store.MetadataSummary{}, context.DeadlineExceeded
		}
		return read(ctx, action)
	}
	if err = m.Start("catalog", nil); err != nil {
		t.Fatal(err)
	}
	if err = m.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	v := m.Snapshot()
	if v.State != "completed" || v.Held || !v.CountsStale || v.Running {
		t.Fatal(v)
	}
	jobs, err := s.MaintenanceJobs(ctx, "catalog")
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if j.State != "completed" {
			t.Fatal(j)
		}
	}
	m.readSummary = read
	if err = m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	v = m.Snapshot()
	if v.CountsStale || v.State != "completed" || v.Held {
		t.Fatal(v)
	}
	// The persisted state agrees with the UI after reopening, without a rerun.
	reopened, err := New(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Snapshot().Held {
		t.Fatal("completed task held on restart")
	}
}
