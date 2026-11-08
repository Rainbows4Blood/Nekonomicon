package enrichment

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Rainbows4Blood/Nekonomicon/internal/enrichdata"
	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
)

func TestUnavailableCoversStillEnrichAndClearOldIssues(t *testing.T) {
	for _, mode := range []string{"unrecorded", "missing-file", "invalid-image", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			s := sampleStore(t, mode != "unrecorded")
			ctx := context.Background()
			for _, source := range []string{"dtrpg", "dmsguild"} {
				path := filepath.Join(s.Dir, "covers", source, "1", "image.png")
				var err error
				switch mode {
				case "missing-file":
					err = os.Remove(path)
				case "invalid-image":
					err = os.WriteFile(path, []byte("not an image"), 0600)
				case "oversized":
					err = os.Truncate(path, (20<<20)+1)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			old, err := s.NextEnrichment(ctx, "", 0)
			if err != nil || old == nil {
				t.Fatal(err)
			}
			if err = s.SaveEnrichment(ctx, *old, enrichdata.Result{}, "", "Cover missing. Download covers and try again"); err != nil {
				t.Fatal(err)
			}
			var first, web atomic.Int64
			provider := fakeProvider{func(_ context.Context, b store.EnrichmentBook, img string, fallback bool) (enrichdata.Result, error) {
				if img != "" || len(b.Raw) == 0 {
					t.Error("invalid coverless input")
				}
				if fallback {
					web.Add(1)
				} else {
					first.Add(1)
				}
				return enrichdata.Result{}, nil
			}}
			r := &Runner{}
			if err = r.Start(ctx, s, provider); err != nil {
				t.Fatal(err)
			}
			wait(t, r)
			c, err := s.EnrichmentCounts(ctx)
			if err != nil || c.Pending != 0 || c.Failed != 0 || c.Unknown != 2 || first.Load() != 2 || web.Load() != 2 {
				t.Fatal(c, r.Snapshot(), first.Load(), web.Load(), err)
			}
			history, err := s.EnrichmentHistory(ctx, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, b := range history {
				if b.Message != "" || b.Status != "complete" {
					t.Fatal("old cover issue retained", b.Message, b.Status)
				}
			}
			if err = r.Start(ctx, s, provider); err != nil {
				t.Fatal(err)
			}
			wait(t, r)
			if first.Load() != 2 || web.Load() != 2 {
				t.Fatal("repeated completed unknowns")
			}
		})
	}
}

func TestOptionalCoverRetainsCancellationAndDatabaseFailures(t *testing.T) {
	s := sampleStore(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := coverImage(ctx, s, store.EnrichmentBook{Source: "dtrpg", ID: 1}); err == nil {
		t.Fatal("cancel ignored")
	}
	s.Close()
	if _, _, err := coverImage(context.Background(), s, store.EnrichmentBook{Source: "dtrpg", ID: 1}); err == nil {
		t.Fatal("database failure treated as absent cover")
	}
}
