package store

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func runTest(t *testing.T, s *Store, id string) Run {
	t.Helper()
	r := Run{ID: id, Mode: "refresh", Phase: "fetch", State: "fetching", Started: now()}
	if e := s.CreateRun(r); e != nil {
		t.Fatal(e)
	}
	return r
}
func TestLosslessRollbackAndRecovery(t *testing.T) {
	s := openTest(t)
	r := runTest(t, s, "first")
	body, e := os.ReadFile("../../docs/fixtures/dtrpg-product-584455.json")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Commit(context.Background(), r.ID, []Result{{ID: 584455, JSON: body}, {ID: 99, JSON: []byte(`{"data":{}}`)}}); e != nil {
		t.Fatal(e)
	}
	got, _ := s.Response(584455)
	if !bytes.Equal(got, body) || !bytes.Contains(got, []byte(`"included"`)) {
		t.Fatal("response bytes changed")
	}
	if _, e = s.Commit(context.Background(), r.ID, []Result{{ID: 100, JSON: body}, {ID: -1, JSON: body}}); e == nil {
		t.Fatal("invalid batch committed")
	}
	if _, e = s.Response(100); e == nil {
		t.Fatal("partial batch survived rollback")
	}
	if _, e = s.Commit(context.Background(), r.ID, []Result{{ID: 584455, Failure: &Failure{Phase: "product", Kind: "restricted", Status: 403, Message: "restricted"}}}); e != nil {
		t.Fatal(e)
	}
	got, _ = s.Response(584455)
	if !bytes.Equal(got, body) {
		t.Fatal("failed refresh replaced last good JSON")
	}
	ids, e := s.Eligibility(r, []int64{1, 99, 100, 584455})
	if e != nil || fmt.Sprint(ids) != "[1 100]" {
		t.Fatalf("out-of-order resume: %v %v", ids, e)
	}
	s.UpdateRun(r.ID, "fetch", "completed_errors", true)
	r = runTest(t, s, "second")
	if _, e = s.Commit(context.Background(), r.ID, []Result{{ID: 584455, JSON: body}}); e != nil {
		t.Fatal(e)
	}
	failures, e := s.Failures(0, 10)
	if e != nil || len(failures) != 0 {
		t.Fatal("success did not clear failure")
	}
	r.Mode = "new"
	ids, e = s.Eligibility(r, []int64{1, 99, 584455})
	if e != nil || fmt.Sprint(ids) != "[1]" {
		t.Fatalf("new mode: %v %v", ids, e)
	}
}
func TestSingleWriterAndActiveRun(t *testing.T) {
	s := openTest(t)
	runTest(t, s, "one")
	if _, e := Open(s.Dir); e == nil {
		t.Fatal("second writer opened")
	}
	if e := s.CreateRun(Run{ID: "two", Mode: "new", Phase: "discover", State: "discovering"}); e == nil {
		t.Fatal("second run opened")
	}
}
func TestCrashRecovery(t *testing.T) {
	if dir := os.Getenv("NEKONOMICON_CRASH_TEST"); dir != "" {
		s, e := Open(dir)
		if e != nil {
			os.Exit(3)
		}
		if s.CreateRun(Run{ID: "crash", Mode: "refresh", Phase: "fetch", State: "fetching", Started: now()}) != nil {
			os.Exit(4)
		}
		if _, e = s.Commit(context.Background(), "crash", []Result{{ID: 3, JSON: []byte(`{"committed":true}`)}}); e != nil {
			os.Exit(5)
		}
		tx, e := s.db.Begin()
		if e != nil {
			os.Exit(6)
		}
		_, e = tx.Exec(`INSERT INTO products VALUES('dtrpg',2,'now','crash','{}')`)
		if e != nil {
			os.Exit(7)
		}
		os.Exit(0)
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashRecovery$")
	cmd.Env = append(os.Environ(), "NEKONOMICON_CRASH_TEST="+dir)
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("child: %s %v", b, e)
	}
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	r, e := s.Active()
	if e != nil || r == nil {
		t.Fatal("unfinished run lost")
	}
	ids, e := s.Eligibility(*r, []int64{1, 2, 3})
	if e != nil || fmt.Sprint(ids) != "[1 2]" {
		t.Fatalf("crash skipped IDs: %v %v", ids, e)
	}
}
func BenchmarkStorage(b *testing.B) {
	body, e := os.ReadFile("../../docs/fixtures/dtrpg-product-584455.json")
	if e != nil {
		b.Fatal(e)
	}
	s, e := Open(filepath.Join(b.TempDir(), "catalog"))
	if e != nil {
		b.Fatal(e)
	}
	defer s.Close()
	if e = s.CreateRun(Run{ID: "bench", Mode: "refresh", Phase: "fetch", State: "fetching", Started: now()}); e != nil {
		b.Fatal(e)
	}
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	batch := make([]Result, 0, 800)
	for i := 0; i < b.N; i++ {
		batch = append(batch, Result{ID: int64(i + 1), JSON: body})
		if len(batch) == 800 || i == b.N-1 {
			if _, e = s.Commit(context.Background(), "bench", batch); e != nil {
				b.Fatal(e)
			}
			batch = batch[:0]
		}
	}
}
