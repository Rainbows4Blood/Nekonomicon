package main

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
)

func TestLocalCoverThumbnailScopeAndBounds(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.CreateRun(store.Run{ID: "seed", Mode: "new"}); err != nil {
		t.Fatal(err)
	}
	relative := "covers/dtrpg/42/cover.png"
	path := filepath.Join(s.Dir, relative)
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 240, 340)))
	if err = os.WriteFile(path, raw.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Commit(context.Background(), "seed", []store.Result{{ID: 42, Cover: &store.Cover{Path: relative, URL: "synthetic"}}}); err != nil {
		t.Fatal(err)
	}
	a := &App{store: s}
	url := "/catalog/cover?catalog=" + catalogKey(s) + "&source=dtrpg&id=42"
	req := httptest.NewRequest("GET", url, nil)
	res := httptest.NewRecorder()
	a.coverHandler(res, req)
	if res.Code != 200 || res.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatal(res.Code)
	}
	cfg, _, err := image.DecodeConfig(res.Body)
	if err != nil || cfg.Width > 120 || cfg.Height > 170 {
		t.Fatal(cfg, err)
	}
	req = httptest.NewRequest("GET", url, nil)
	req.Header.Set("If-None-Match", res.Header().Get("ETag"))
	res = httptest.NewRecorder()
	a.coverHandler(res, req)
	if res.Code != 304 {
		t.Fatal("cache validation failed")
	}
	for _, bad := range []string{"/catalog/cover?source=dtrpg&id=42", strings.Replace(url, "source=dtrpg", "source=../dtrpg", 1), "/catalog/cover?catalog=" + catalogKey(s) + "&source=dmsguild&id=42"} {
		res = httptest.NewRecorder()
		a.coverHandler(res, httptest.NewRequest("GET", bad, nil))
		if res.Code != 404 {
			t.Fatal("invalid scope returned cover", bad, res.Code)
		}
	}
	if _, err = s.Commit(context.Background(), "seed", []store.Result{{ID: 42, Cover: &store.Cover{Path: "covers/dtrpg/42/../42/cover.png", URL: "synthetic"}}}); err != nil {
		t.Fatal(err)
	}
	res = httptest.NewRecorder()
	a.coverHandler(res, httptest.NewRequest("GET", url, nil))
	if res.Code != 404 {
		t.Fatal("noncanonical cover path accepted")
	}
}
