package analysis

import (
	"os"
	"path/filepath"
	"strings"
)

// WalkGoFiles walks dir recursively and calls fn for each .go file found.
// Skips vendor/, testdata/, hidden directories, and _test.go files.
func WalkGoFiles(dir string, fn func(path string) error) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}

		if d.IsDir() {
			name := d.Name()
			if name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}

		return fn(path)
	})
}
