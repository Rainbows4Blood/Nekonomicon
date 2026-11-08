package enrichment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Rainbows4Blood/Nekonomicon/internal/store"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

func coverImage(ctx context.Context, s *store.Store, b store.EnrichmentBook) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	path, err := s.CoverPath(ctx, b.Source, b.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	if !filepath.IsLocal(path) || filepath.ToSlash(filepath.Clean(path)) != filepath.ToSlash(path) || !strings.HasPrefix(filepath.ToSlash(path), fmt.Sprintf("covers/%s/%d/", b.Source, b.ID)) {
		return "", "", nil
	}
	root, err := os.OpenRoot(s.Dir)
	if err != nil {
		return "", "", nil
	}
	defer root.Close()
	f, err := root.Open(path)
	if err != nil {
		return "", "", nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 20<<20 {
		return "", "", nil
	}
	raw, err := io.ReadAll(io.LimitReader(f, (20<<20)+1))
	if err != nil || len(raw) > 20<<20 {
		return "", "", nil
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 16000000 {
		return "", "", nil
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", "", nil
	}
	w, h := cfg.Width, cfg.Height
	if max(w, h) > 1600 {
		if w > h {
			h = max(1, h*1600/w)
			w = 1600
		} else {
			w = max(1, w*1600/h)
			h = 1600
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)
	var out bytes.Buffer
	if jpeg.Encode(&out, dst, &jpeg.Options{Quality: 90}) != nil {
		return "", "", nil
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(out.Bytes()), fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}
