package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
	"github.com/Rainbows4Blood/Nekonomicon/internal/storefront"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	xdraw "golang.org/x/image/draw"
)

func catalogKey(s *store.Store) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s.Dir))) }
func (a *App) catalogStore() (*store.Store, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.loading || a.closing || a.store == nil || a.store.Version() != store.SchemaVersion {
		return nil, errors.New("Open a compatible catalog and apply the database update in Data maintenance")
	}
	return a.store, nil
}
func (a *App) BrowseBooks(query store.BookQuery) (store.BookPage, error) {
	s, err := a.catalogStore()
	if err != nil {
		return store.BookPage{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	page, err := s.Books(ctx, query)
	page.CatalogKey = catalogKey(s)
	return page, err
}
func (a *App) AdventureFilterOptions(kind, after string) (store.EnrichmentOptions, error) {
	s, err := a.catalogStore()
	if err != nil {
		return store.EnrichmentOptions{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := s.EnrichmentOptions(ctx, kind, after)
	result.CatalogKey = catalogKey(s)
	return result, err
}
func (a *App) BookDetails(source string, id int64) (store.BookDetail, error) {
	s, err := a.catalogStore()
	if err != nil {
		return store.BookDetail{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	d, err := s.BookDetails(ctx, storefront.Source(source), id)
	d.CatalogKey = catalogKey(s)
	return d, err
}
func (a *App) OpenBook(source string, id int64) error {
	s := storefront.Source(source)
	if !s.Valid() || id <= 0 {
		return errors.New("Unknown book")
	}
	wruntime.BrowserOpenURL(a.ctx, s.ProductURL(id))
	return nil
}
func (a *App) CatalogIssues(offset int) ([]store.MetadataIssue, error) {
	s, err := a.catalogStore()
	if err != nil {
		return []store.MetadataIssue{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.CatalogIssues(ctx, "", offset)
}

// Image decoding is bounded separately from database work. Only recorded local
// cover files are served; the UI never sends paths and never loads remote images.
var thumbnailSlots = make(chan struct{}, 2)

func (a *App) coverHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" || r.URL.Path != "/catalog/cover" {
		http.NotFound(w, r)
		return
	}
	s, err := a.catalogStore()
	if err != nil || r.URL.Query().Get("catalog") != catalogKey(s) {
		http.NotFound(w, r)
		return
	}
	source := storefront.Source(r.URL.Query().Get("source"))
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || !source.Valid() || id <= 0 {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	relative, err := s.CoverPath(ctx, source, id)
	prefix := fmt.Sprintf("covers/%s/%d/", source, id)
	if err != nil || !strings.HasPrefix(filepath.ToSlash(relative), prefix) || !filepath.IsLocal(relative) || filepath.ToSlash(filepath.Clean(relative)) != filepath.ToSlash(relative) {
		http.NotFound(w, r)
		return
	}
	// OpenRoot rejects traversal and symlinks that escape the catalog directory.
	root, err := os.OpenRoot(s.Dir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	f, err := root.Open(filepath.FromSlash(relative))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 20<<20 {
		http.NotFound(w, r)
		return
	}
	width, height := 120, 170
	if r.URL.Query().Get("size") == "detail" {
		width, height = 400, 560
	}
	etag := fmt.Sprintf(`"%x-%d-%d"`, info.ModTime().UnixNano(), info.Size(), width)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	select {
	case thumbnailSlots <- struct{}{}:
		defer func() { <-thumbnailSlots }()
	case <-ctx.Done():
		http.Error(w, "Image busy", http.StatusServiceUnavailable)
		return
	}
	cfg, _, err := image.DecodeConfig(io.LimitReader(f, 20<<20))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 16_000_000 {
		http.NotFound(w, r)
		return
	}
	if _, err = f.Seek(0, 0); err != nil {
		http.NotFound(w, r)
		return
	}
	img, _, err := image.Decode(io.LimitReader(f, 20<<20))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if cfg.Width*height > cfg.Height*width {
		height = max(1, cfg.Height*width/cfg.Width)
	} else {
		width = max(1, cfg.Width*height/cfg.Height)
	}
	thumbnail := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(thumbnail, thumbnail.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
	xdraw.ApproxBiLinear.Scale(thumbnail, thumbnail.Bounds(), img, img.Bounds(), draw.Over, nil)
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = jpeg.Encode(w, thumbnail, &jpeg.Options{Quality: 85})
}
