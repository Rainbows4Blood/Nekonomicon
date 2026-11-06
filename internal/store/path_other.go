//go:build !windows

package store

import (
	"os"
	"path/filepath"
)

func physicalFilePath(file *os.File) (string, error) {
	return filepath.EvalSymlinks(file.Name())
}
