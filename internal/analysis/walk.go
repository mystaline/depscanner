// Package analysis provides utilities for traversing and analyzing Go source code.
package analysis

import (
	"os"
	"path/filepath"
	"strings"
)

// WalkGoFiles performs a recursive traversal of the specified directory
// and executes the provided function for every Go source file found.
//
// It automatically filters out irrelevant directories and files to speed up
// the scanning process and avoid issues with dependency management.
//
// Filtered items:
// - vendor/ directory
// - testdata/ directory
// - Hidden directories (starting with '.')
// - Test files (ending with _test.go)
func WalkGoFiles(dir string, fn func(path string) error) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Skip unreadable entries or permission errors
		}

		if d.IsDir() {
			name := d.Name()
			// Skip specialized directories to avoid non-source or vendor noise.
			if name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}

		// Process only Go source files, skipping test files.
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}

		return fn(path)
	})
}
