package dtrpg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "golang.org/x/image/webp"
)

func ImageURL(path string) (string, error) {
	bad := Fail("cover", "Primary image path is outside the approved CDN scope")
	if path == "" {
		return "", nil
	}
	if strings.Contains(path, "\\") {
		return "", bad
	}
	u, e := url.Parse(path)
	if e != nil || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return "", bad
	}
	if u.IsAbs() {
		if u.Scheme != "https" || u.Host != "d1vzi28wh99zvq.cloudfront.net" {
			return "", bad
		}
	} else if u.Host != "" || strings.HasPrefix(u.Path, "/") {
		return "", bad
	}
	for _, p := range strings.Split(u.Path, "/") {
		if p == ".." || p == "." || strings.Contains(p, "\\") {
			return "", bad
		}
	}
	base, _ := url.Parse(CDN + "images/")
	if !u.IsAbs() && strings.HasPrefix(u.Path, "images/") {
		base, _ = url.Parse(CDN)
	}
	return base.ResolveReference(u).String(), nil
}
func ImageExtension(b []byte, contentType string) (string, error) {
	bad := Fail("cover", "Cover response is not a complete supported image")
	typ := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if typ != "image/png" && typ != "image/jpeg" && typ != "image/jpg" && typ != "image/gif" && typ != "image/webp" && typ != "application/octet-stream" {
		return "", bad
	}
	cfg, format, e := image.DecodeConfig(bytes.NewReader(b))
	if e != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 100_000_000 {
		return "", bad
	}
	switch format {
	case "png":
		if !bytes.HasSuffix(b, []byte{0, 0, 0, 0, 'I', 'E', 'N', 'D', 174, 66, 96, 130}) {
			return "", bad
		}
		return ".png", nil
	case "jpeg":
		if !bytes.HasSuffix(b, []byte{255, 217}) {
			return "", bad
		}
		return ".jpg", nil
	case "gif":
		if b[len(b)-1] != ';' {
			return "", bad
		}
		return ".gif", nil
	case "webp":
		if len(b) < 12 || int64(binary.LittleEndian.Uint32(b[4:8]))+8 != int64(len(b)) {
			return "", bad
		}
		return ".webp", nil
	}
	return "", bad
}

// SaveCover writes and syncs beside the destination, then atomically replaces
// it. A failed transfer or rename leaves the previous file untouched.
func SaveCover(root string, id int64, source string, b []byte, contentType string) (string, error) {
	if id <= 0 || len(b) > CoverLimit {
		return "", Fail("cover", "Invalid cover size or product ID")
	}
	ext, e := ImageExtension(b, contentType)
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256([]byte(source))
	relative := filepath.Join("covers", "dtrpg", fmt.Sprint(id), fmt.Sprintf("%x%s", sum[:16], ext))
	dest := filepath.Join(root, relative)
	if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
		return "", Fail("storage", "Cannot create cover folder; check free space and permissions")
	}
	f, e := os.CreateTemp(filepath.Dir(dest), ".cover-*")
	if e != nil {
		return "", Fail("storage", "Cannot create cover file; check free space and permissions")
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e == nil {
		e = os.Rename(tmp, dest)
	}
	if e != nil {
		return "", Fail("storage", "Cannot save cover; check free space and permissions")
	}
	return filepath.ToSlash(relative), nil
}
func CoverExists(root, relative string) bool {
	if relative == "" || !filepath.IsLocal(filepath.FromSlash(relative)) {
		return false
	}
	info, e := os.Stat(filepath.Join(root, filepath.FromSlash(relative)))
	return e == nil && info.Mode().IsRegular() && info.Size() > 0
}
func (n *Network) DownloadCover(ctx, starts context.Context, root string, id int64, source string) (string, error) {
	r, e := n.GetForRun(ctx, starts, source, 0, CoverLimit, false, true)
	if e != nil {
		return "", e
	}
	return SaveCover(root, id, source, r.Body, r.Type)
}
