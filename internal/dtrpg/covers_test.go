package dtrpg

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestCoverReplacement(t *testing.T) {
	root := t.TempDir()
	im := image.NewRGBA(image.Rect(0, 0, 2, 2))
	im.Set(0, 0, color.RGBA{R: 255, A: 255})
	var b bytes.Buffer
	png.Encode(&b, im)
	source := CDN + "123/456.png"
	path, e := SaveCover(root, 456, source, b.Bytes(), "image/png")
	if e != nil {
		t.Fatal(e)
	}
	old, _ := os.ReadFile(filepath.Join(root, path))
	if !CoverExists(root, path) {
		t.Fatal("saved cover missing")
	}
	if _, e = SaveCover(root, 456, source, []byte("<html>failed</html>"), "image/png"); e == nil {
		t.Fatal("HTML accepted")
	}
	if _, e = SaveCover(root, 456, source, b.Bytes()[:len(b.Bytes())-4], "image/png"); e == nil {
		t.Fatal("partial PNG accepted")
	}
	after, _ := os.ReadFile(filepath.Join(root, path))
	if !bytes.Equal(old, after) {
		t.Fatal("old cover damaged")
	}
	files, _ := filepath.Glob(filepath.Join(root, "covers/dtrpg/456/.cover-*"))
	if len(files) > 0 {
		t.Fatal("temporary file leaked")
	}
	if _, e = SaveCover(root, 456, source, b.Bytes(), "image/png"); e != nil {
		t.Fatal("atomic same-name replacement", e)
	}
}
func TestCoverPaths(t *testing.T) {
	for _, s := range []string{"../x.png", "%2e%2e/x.png", "/x.png", "//evil.invalid/x.png", "https://evil.invalid/x.png", "a\\b.png", "https://d1vzi28wh99zvq.cloudfront.net:443/x.png", "a.png?token=secret"} {
		if _, e := ImageURL(s); e == nil {
			t.Errorf("unsafe path %s", s)
		}
	}
	if u, e := ImageURL("23458/584455.png"); e != nil || u != CDN+"images/23458/584455.png" {
		t.Fatal(u, e)
	}
}

func TestLegacyJPEGContentType(t *testing.T) {
	var b bytes.Buffer
	_ = jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil)
	if ext, err := ImageExtension(b.Bytes(), "image/jpg"); err != nil || ext != ".jpg" {
		t.Fatal("valid legacy MIME rejected", err)
	}
}
