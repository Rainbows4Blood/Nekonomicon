package enrichment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Rainbows4Blood/Nekonomicon/internal/enrichdata"
	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
)

func number(n int) *int { return &n }
func complete() enrichdata.Result {
	return enrichdata.Result{Players: enrichdata.Range{Min: number(3), Max: number(5), Evidence: "For 3–5 players", Sources: []string{}}, Levels: enrichdata.Range{Min: number(1), Max: number(4), Evidence: "Levels 1–4", Sources: []string{}}, Settings: enrichdata.Settings{Names: []string{"Forgotten Realms"}, Evidence: "Set in the Forgotten Realms"}, Locations: enrichdata.Locations{Items: []enrichdata.Location{{Name: "Waterdeep"}}, Evidence: "Begins in Waterdeep"}, Dates: enrichdata.Dates{Items: []enrichdata.WorldDate{{Date: "1492 DR"}}, Evidence: "In 1492 DR"}}
}
func sampleStore(t *testing.T, cover bool) *store.Store { return sampleCatalog(t, cover, 2) }
func sampleCatalog(t *testing.T, cover bool, size int64) *store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, source := range []storefront.Source{storefront.DTRPG, storefront.DMSGuild} {
		run := string(source)
		if err = s.CreateRun(store.Run{ID: run, Source: source, Mode: "new"}); err != nil {
			t.Fatal(err)
		}
		for id := int64(1); id <= size; id++ {
			label := "Adventures"
			if id == 2 {
				label = "Rulebooks"
			}
			raw := []byte(fmt.Sprintf(`{"productId":%d,"description":{"name":"The adventure of a test","description":"For 3-5 players"},"filters":[{"filterId":44705},{"filterId":777,"descriptions":[{"name":%q}]}]}`, id, label))
			result := store.Result{ID: id, JSON: raw}
			if cover {
				rel := fmt.Sprintf("covers/%s/%d/image.png", source, id)
				path := filepath.Join(s.Dir, rel)
				os.MkdirAll(filepath.Dir(path), 0700)
				var imageBytes bytes.Buffer
				png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 24, 34)))
				os.WriteFile(path, imageBytes.Bytes(), 0600)
				if _, err = s.Commit(context.Background(), run, []store.Result{result}); err != nil {
					t.Fatal(err)
				}
				result = store.Result{ID: id, Cover: &store.Cover{Path: rel, URL: "synthetic"}}
			}
			if _, err = s.Commit(context.Background(), run, []store.Result{result}); err != nil {
				t.Fatal(err)
			}
		}
		if err = s.UpdateRun(run, "fetch", "completed", true); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

type fakeProvider struct {
	call func(context.Context, store.EnrichmentBook, string, bool) (enrichdata.Result, error)
}

func (f fakeProvider) Check(context.Context) error { return nil }
func (f fakeProvider) Extract(ctx context.Context, b store.EnrichmentBook, img string, web bool, _ func(string)) (enrichdata.Result, error) {
	return f.call(ctx, b, img, web)
}
func wait(t *testing.T, r *Runner) {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(8 * time.Second):
		t.Fatal("runner stuck")
	}
}
func TestMissingOnlyBothStoresAndConditionalWeb(t *testing.T) {
	s := sampleStore(t, true)
	var calls, webCalls atomic.Int64
	p := fakeProvider{}
	// The web response cannot overwrite populated fields. Return a valid but
	// different range to verify the merge, while unknown fields are filled.
	p.call = func(ctx context.Context, b store.EnrichmentBook, img string, web bool) (enrichdata.Result, error) {
		calls.Add(1)
		if !strings.HasPrefix(img, "data:image/jpeg;base64,") || !json.Valid(b.Raw) {
			t.Error("missing inputs")
		}
		r := complete()
		if !web {
			r.Levels = enrichdata.Range{}
		} else {
			webCalls.Add(1)
			if !b.Existing.Players.Complete() {
				t.Error("missing prior values")
			}
			r.Players.Min, r.Players.Max = number(9), number(10)
		}
		return r, nil
	}
	r := &Runner{}
	r.Start(context.Background(), s, p)
	wait(t, r)
	if calls.Load() != 4 || webCalls.Load() != 2 || r.Snapshot().Enriched != 2 {
		t.Fatal(calls.Load(), webCalls.Load(), r.Snapshot())
	}
	counts, err := s.EnrichmentCounts(context.Background())
	if err != nil || counts.Adventures != 2 || counts.Pending != 0 {
		t.Fatal(counts, err)
	}
	for _, source := range []storefront.Source{storefront.DTRPG, storefront.DMSGuild} {
		v, err := s.BookEnrichment(context.Background(), source, 1)
		if err != nil || *v.Players.Min != 3 {
			t.Fatal(v, err)
		}
	}
	r.Start(context.Background(), s, p)
	wait(t, r)
	if calls.Load() != 4 {
		t.Fatal("complete books sent again")
	}
}
func TestStorefrontCompleteSkipsSearchWithOrWithoutCover(t *testing.T) {
	for _, hasCover := range []bool{false, true} {
		t.Run(fmt.Sprint(hasCover), func(t *testing.T) {
			s := sampleStore(t, hasCover)
			var calls atomic.Int64
			p := fakeProvider{func(_ context.Context, _ store.EnrichmentBook, img string, web bool) (enrichdata.Result, error) {
				calls.Add(1)
				if (img != "") != hasCover {
					t.Error("wrong optional image input")
				}
				if web {
					t.Error("unnecessary web pass")
				}
				return complete(), nil
			}}
			r := &Runner{}
			r.Start(context.Background(), s, p)
			wait(t, r)
			if calls.Load() != 2 {
				t.Fatal(calls.Load())
			}
			if r.Snapshot().Failed != 0 || r.Snapshot().Enriched != 2 {
				t.Fatal(r.Snapshot())
			}
		})
	}
}
func TestCancellationRetainsFirstPassAndRetryOnlyMissingFields(t *testing.T) {
	s := sampleStore(t, true)
	entered := make(chan struct{}, 2)
	r := &Runner{}
	p := fakeProvider{func(ctx context.Context, _ store.EnrichmentBook, _ string, web bool) (enrichdata.Result, error) {
		if web {
			entered <- struct{}{}
			<-ctx.Done()
			return enrichdata.Result{}, ctx.Err()
		}
		v := complete()
		v.Levels = enrichdata.Range{}
		return v, nil
	}}
	r.Start(context.Background(), s, p)
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("no web pass started")
		}
	}
	r.Stop()
	wait(t, r)
	b, err := s.NextEnrichment(context.Background(), "", 0)
	if err != nil || b == nil || !b.Existing.Players.Complete() || b.Existing.Levels.Complete() {
		t.Fatal(b, err)
	}
	var seenExisting atomic.Bool
	r.Start(context.Background(), s, fakeProvider{func(_ context.Context, b store.EnrichmentBook, _ string, _ bool) (enrichdata.Result, error) {
		if b.Existing.Players.Complete() {
			seenExisting.Store(true)
		}
		return complete(), nil
	}})
	wait(t, r)
	if !seenExisting.Load() || r.Snapshot().Enriched != 2 {
		t.Fatal(r.Snapshot())
	}
}
func terminal(result enrichdata.Result) string {
	output := map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": result.JSON()}}}}}}
	b, _ := json.Marshal(output)
	return "data: " + string(b) + "\n\n"
}
func TestHTTPContractAndInterruptedStream(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic" {
			t.Error("auth missing")
		}
		if r.URL.Path == "/models" {
			fmt.Fprint(w, `{"models":[{"slug":"gpt-6.1-terra"}]}`)
			return
		}
		calls.Add(1)
		var req map[string]any
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			t.Error("invalid request")
		}
		if req["model"] != enrichdata.Model || req["service_tier"] != "priority" || req["store"] != false || req["stream"] != true {
			t.Error("invalid model/transport")
		}
		if (req["tools"] != nil) != (calls.Load()%2 == 0) {
			t.Error("search enabled in first pass")
		}
		input, _ := json.Marshal(req["input"])
		if calls.Load() > 2 && (bytes.Contains(input, []byte("input_image")) || !bytes.Contains(input, []byte("No cover image is available"))) {
			t.Error("invalid coverless request")
		}
		if !bytes.Contains(input, []byte("TOP_LEVEL_INCLUDED_SENTINEL")) || (bytes.Contains(input, []byte("data:image/jpeg;base64,")) != (calls.Load() <= 2)) {
			t.Error("source truncated")
		}
		fmt.Fprint(w, terminal(complete()))
	}))
	defer srv.Close()
	c := NewClient(func(context.Context) (string, error) { return "synthetic", nil })
	c.Base = srv.URL
	if err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, cover := range []string{"data:image/jpeg;base64,synthetic", ""} {
		for _, web := range []bool{false, true} {
			_, err := c.Extract(context.Background(), store.EnrichmentBook{Source: storefront.DTRPG, ID: 1, Raw: []byte(`{"included":"TOP_LEVEL_INCLUDED_SENTINEL"}`)}, cover, web, nil)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, raw := range []string{"data: {\"type\":\"response.output_text.delta\",\"delta\":\"{}\"}\n\n", "data: {\"type\":\"response.incomplete\"}\n\n", "data: {\"type\":\"response.failed\"}\n\n"} {
		if _, err := readStream(strings.NewReader(raw), nil); err == nil {
			t.Fatal("partial/failed response accepted")
		}
	}
	bad := complete()
	bad.Levels.Min = number(10)
	if _, err := readStream(strings.NewReader(terminal(bad)), nil); err == nil {
		t.Fatal("inverted range accepted")
	}
}
func TestStopErrorsDoNotProcessWholeCatalog(t *testing.T) {
	s := sampleCatalog(t, true, workerCount+1)
	var calls atomic.Int64
	r := &Runner{}
	r.Start(context.Background(), s, fakeProvider{func(context.Context, store.EnrichmentBook, string, bool) (enrichdata.Result, error) {
		calls.Add(1)
		return enrichdata.Result{}, StopError{"OpenAI access denied"}
	}})
	wait(t, r)
	if calls.Load() < 1 || calls.Load() > workerCount || r.Snapshot().State != "blocked" {
		t.Fatal(r.Snapshot())
	}
}
