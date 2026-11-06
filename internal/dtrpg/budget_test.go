package dtrpg

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestPauseDoesNotResetProductAttemptBudget(t *testing.T) {
	calls := 0
	var cancel context.CancelFunc
	n := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fmt.Fprint(w, "User-agent: *\nAllow: /\n")
			return
		}
		calls++
		if calls <= 3 {
			cancel()
			w.WriteHeader(503)
		} else {
			fmt.Fprint(w, `{"productId":1,"image":null}`)
		}
	})
	n.Attempts = 3
	n.BeginRun("same-run")
	if _, err := n.Robots(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		var starts context.Context
		starts, cancel = context.WithCancel(context.Background())
		if _, err := n.GetForRun(context.Background(), starts, ProductURL(1), 1, 1024, false, false); err == nil {
			t.Fatal("unexpected success")
		}
		cancel()
		n.BeginRun("same-run")
	}
	if _, err := n.GetForRun(context.Background(), context.Background(), ProductURL(1), 1, 1024, false, false); err == nil || FailureOf(err).Kind != "attempts" {
		t.Fatal("budget did not survive pause")
	}
	if calls != 3 {
		t.Fatal("more than three attempts in one run")
	}
	n.BeginRun("new-run")
	if _, err := n.GetForRun(context.Background(), context.Background(), ProductURL(1), 1, 1024, false, false); err != nil || calls != 4 {
		t.Fatal("new run did not get a fresh budget")
	}
}
