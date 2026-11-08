package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Test the files compiled into the executable, not the checkout served by a dev
// browser. A missing transitive import prevents all UI handlers from starting.
func TestEmbeddedFrontendDependencies(t *testing.T) {
	files, err := fs.Sub(assets, "frontend")
	if err != nil {
		t.Fatal(err)
	}
	patterns := map[string][]*regexp.Regexp{
		".html": {regexp.MustCompile(`(?:src|href)=["']([^"']+)["']`)},
		".js":   {regexp.MustCompile(`(?m)^\s*(?:import|export)\b[^\r\n;]*?\bfrom\s+["']([^"']+)["']`), regexp.MustCompile(`(?m)^\s*import\s+["']([^"']+)["']`), regexp.MustCompile(`\bimport\(\s*["']([^"']+)["']`)},
		".css":  {regexp.MustCompile(`url\(["']?([^"')]+)`)},
	}
	seen := map[string]bool{}
	var visit func(string)
	visit = func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		body, e := fs.ReadFile(files, name)
		if e != nil {
			t.Errorf("embedded frontend dependency %s is missing: %v", name, e)
			return
		}
		switch path.Ext(name) {
		case ".html", ".js", ".css":
		default:
			return
		}
		for _, pattern := range patterns[path.Ext(name)] {
			for _, match := range pattern.FindAllSubmatch(body, -1) {
				ref := string(match[1])
				if strings.Contains(ref, ":") || strings.HasPrefix(ref, "#") {
					continue
				}
				ref = strings.SplitN(strings.SplitN(ref, "?", 2)[0], "#", 2)[0]
				if strings.HasPrefix(ref, "/") {
					ref = strings.TrimPrefix(ref, "/")
				} else {
					ref = path.Join(path.Dir(name), ref)
				}
				visit(ref)
			}
		}
	}
	visit("index.html")
}

// Optional browser check consumes only the embedded payload. The environment
// variable points to an installed Playwright package, avoiding a runtime dependency.
func TestPackagedFrontendBrowser(t *testing.T) {
	if os.Getenv("NEKONOMICON_PLAYWRIGHT") == "" {
		t.Skip("set NEKONOMICON_PLAYWRIGHT to an installed Playwright package")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	files, err := fs.Sub(assets, "frontend")
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string][]byte{}
	err = fs.WalkDir(files, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		payload[name], err = fs.ReadFile(files, name)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "frontend/packaged.test.cjs")
	cmd.Stdin = bytes.NewReader(raw)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("packaged UI failed: %v\n%s", err, output)
	}
	t.Log(string(output))
}
