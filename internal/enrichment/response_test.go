package enrichment

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Rainbows4Blood/Nekonomicon/internal/enrichdata"
)

func message(phase, text string) map[string]any {
	return map[string]any{"type": "message", "role": "assistant", "phase": phase, "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": text}}}
}
func responseStream(items ...any) string {
	b, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": items}})
	return "data: " + string(b) + "\n\n"
}

// ChatGPT-plan streaming may deliver the answer in output_item.done and leave
// response.completed.output empty. Completion is still required before saving it.
func streamedItems(items ...any) string {
	var s strings.Builder
	for i, item := range items {
		b, _ := json.Marshal(map[string]any{"type": "response.output_item.done", "output_index": i, "item": item})
		fmt.Fprintf(&s, "data: %s\n\n", b)
	}
	return s.String()
}

func TestCompletedStreamRetainsAnswerFromEarlierItemEvent(t *testing.T) {
	want := unknownResult()
	stream := streamedItems(
		map[string]any{"type": "web_search_call", "status": "completed"},
		map[string]any{"type": "reasoning"},
		map[string]any{"type": "web_search_call", "status": "completed"},
		message("final_answer", want.JSON()),
	) + responseStream()
	got, err := readStream(strings.NewReader(stream), nil)
	if err != nil || got.JSON() != want.JSON() {
		t.Fatalf("completed streamed answer was discarded: %v", err)
	}
}

func TestStreamedAnswerStillRequiresSuccessfulTerminalEvent(t *testing.T) {
	answer := streamedItems(message("final_answer", unknownResult().JSON()))
	for _, tail := range []string{
		"",
		"data: [DONE]\n\n",
		"data: {\"type\":\"response.failed\"}\n\n",
		"data: {\"type\":\"response.incomplete\"}\n\n",
		"data: {\"type\":\"error\"}\n\n",
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"incomplete\",\"output\":[]}}\n\n",
	} {
		if _, err := readStream(strings.NewReader(answer+tail), nil); err == nil {
			t.Fatal("unfinished stream accepted")
		}
	}
	// Terminal status overrides the same streamed item, and commentary alone
	// never becomes successful unknown metadata merely because the stream ends.
	bad := message("final_answer", unknownResult().JSON())
	bad["status"] = "incomplete"
	if _, err := readStream(strings.NewReader(answer+responseStream(bad)), nil); err == nil {
		t.Fatal("terminal incomplete message ignored")
	}
	if _, err := readStream(strings.NewReader(streamedItems(message("commentary", unknownResult().JSON()))+responseStream()), nil); err == nil {
		t.Fatal("commentary counted as enrichment")
	}
}

func TestStreamedItemsUseOutputPositionAndDoNotDuplicate(t *testing.T) {
	final := message("final_answer", unknownResult().JSON())
	finalEvent, _ := json.Marshal(map[string]any{"type": "response.output_item.done", "output_index": 3, "item": final})
	commentaryEvent, _ := json.Marshal(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": message("commentary", "Searching…")})
	stream := fmt.Sprintf("data: %s\n\ndata: %s\n\ndata: %s\n\n", finalEvent, commentaryEvent, finalEvent) + responseStream()
	if _, err := readStream(strings.NewReader(stream), nil); err != nil {
		t.Fatal("reordered/duplicate events broke final answer", err)
	}
}
func unknownResult() enrichdata.Result {
	return enrichdata.Result{
		Players: enrichdata.Range{Evidence: "No numerical player endpoints specified.", Sources: []string{}},
		Levels:  enrichdata.Range{Evidence: "No numerical level endpoints specified.", Sources: []string{}},
	}
}

func TestWebCommentaryDoesNotCorruptFinalStructuredResult(t *testing.T) {
	for _, phase := range []string{"final_answer", ""} {
		t.Run("final phase="+phase, func(t *testing.T) {
			for _, result := range []enrichdata.Result{unknownResult(), complete()} {
				stream := responseStream(
					message("commentary", "I will check the publisher for any missing ranges."),
					map[string]any{"type": "web_search_call", "status": "completed"},
					message("commentary", "The source does not specify a fixed party size."),
					message(phase, result.JSON()),
				)
				got, err := readStream(strings.NewReader(stream), nil)
				if err != nil || got.JSON() != result.JSON() {
					t.Fatalf("valid final answer rejected: %v", err)
				}
			}
		})
	}
}

func TestOnlyTheFinalAnswerCanCompleteEnrichment(t *testing.T) {
	valid := unknownResult().JSON()
	refusal := message("final_answer", "")
	refusal["content"] = []any{map[string]any{"type": "refusal", "refusal": "synthetic refusal"}}
	incomplete := message("final_answer", valid)
	incomplete["status"] = "incomplete"
	for _, tt := range []struct {
		name      string
		items     []any
		wantError bool
	}{
		{"legacy unphased commentary", []any{message("", "Searching for the missing values."), message("", valid)}, false},
		{"explicit final takes precedence", []any{message("final_answer", valid), message("", "Progress summary")}, false},
		{"commentary is not a result", []any{message("commentary", valid)}, true},
		{"draft cannot replace refusal", []any{message("commentary", valid), refusal}, true},
		{"draft cannot replace incomplete final", []any{message("commentary", valid), incomplete}, true},
		{"earlier JSON cannot replace invalid final", []any{message("", valid), message("final_answer", "Not JSON")}, true},
		{"missing ranges are not unknown ranges", []any{message("final_answer", `{}`)}, true},
		{"missing endpoints are not null endpoints", []any{message("final_answer", `{"players":{"evidence":"Unknown","sources":[]},"levels":{"min":null,"max":null,"evidence":"Unknown","sources":[]}}`)}, true},
		{"tool result alone is not an answer", []any{map[string]any{"type": "web_search_call", "status": "completed"}}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := readStream(strings.NewReader(responseStream(tt.items...)), nil)
			if (err != nil) != tt.wantError {
				t.Fatal("unexpected final answer validation", err)
			}
		})
	}
}

func TestUnknownHTTPFallbackCommitsAndIsNotRetried(t *testing.T) {
	s := sampleStore(t, true)
	// Reproduce the saved checkpoint/error in the report, then retry through
	// the real HTTP client, stream parser, runner and SQLite writer.
	for _, source := range []string{"dmsguild", "dtrpg"} {
		b, err := s.NextEnrichment(context.Background(), "", 0)
		if source == "dtrpg" {
			b, err = s.NextEnrichment(context.Background(), "dmsguild", 1)
		}
		if err != nil || b == nil {
			t.Fatal(b, err)
		}
		if err = s.SaveEnrichment(context.Background(), *b, unknownResult(), "hash", "OpenAI returned incomplete structured output"); err != nil {
			t.Fatal(err)
		}
	}
	before, err := s.EnrichmentCounts(context.Background())
	if err != nil || before.Failed != 2 || before.Pending != 2 {
		t.Fatal(before, err)
	}
	var calls, webCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			fmt.Fprint(w, `{"data":[{"id":"gpt-6.1-terra"}]}`)
			return
		}
		calls.Add(1)
		var req struct {
			Tools []json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		result := unknownResult()
		if len(req.Tools) == 0 {
			fmt.Fprint(w, responseStream(message("final_answer", result.JSON())))
			return
		}
		webCalls.Add(1)
		fmt.Fprint(w, "data: {\"type\":\"response.web_search_call.searching\"}\n\n")
		fmt.Fprint(w, streamedItems(message("commentary", "I will search for the missing ranges."), map[string]any{"type": "web_search_call", "status": "completed"}, message("final_answer", result.JSON())))
		fmt.Fprint(w, responseStream())
	}))
	defer srv.Close()
	c := NewClient(func(context.Context) (string, error) { return "synthetic", nil })
	c.Base = srv.URL
	r := &Runner{}
	t.Cleanup(r.Shutdown)
	r.Start(context.Background(), s, c)
	wait(t, r)
	v := r.Snapshot()
	if v.Enriched != 2 || v.Unknown != 2 || v.Failed != 0 {
		t.Fatal("HTTP unknown answers did not count as enriched", v)
	}
	counts, err := s.EnrichmentCounts(context.Background())
	if err != nil || counts.Pending != 0 || counts.Unknown != 2 || counts.Failed != 0 {
		t.Fatal(counts, err)
	}
	history, err := s.EnrichmentHistory(context.Background(), 0)
	if err != nil || len(history) != 2 {
		t.Fatal(history, err)
	}
	for _, b := range history {
		if b.Status != "complete" || b.Message != "" || !b.PlayersDone || !b.LevelsDone {
			t.Fatal("retry did not clear the old issue", b.Status, b.Message)
		}
	}
	r.Start(context.Background(), s, c)
	wait(t, r)
	if calls.Load() != 4 || webCalls.Load() != 2 {
		t.Fatal("completed unknowns were sent again", calls.Load(), webCalls.Load())
	}
}
