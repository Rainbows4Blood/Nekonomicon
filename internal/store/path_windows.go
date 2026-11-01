package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func physicalFilePath(file *os.File) (string, error) {
	buffer := make([]uint16, 512)
	for {
		n, err := windows.GetFinalPathNameByHandle(windows.Handle(file.Fd()), &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if n < uint32(len(buffer)) {
			path := windows.UTF16ToString(buffer[:n])
			// Keep the familiar drive/UNC form for Explorer and Go's path handling.
			if strings.HasPrefix(path, `\\?\UNC\`) {
				path = `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
			} else {
				path = strings.TrimPrefix(path, `\\?\`)
			}
			return filepath.Clean(path), nil
		}
		if n > 32768 {
			return "", errors.New("Resolved catalog path exceeds the Windows path limit")
		}
		buffer = make([]uint16, n+1)
	}
}
