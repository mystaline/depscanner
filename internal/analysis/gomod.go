// Package analysis provides static analysis utilities for Go source code,
// including go.mod parsing and dependency detection.
package analysis

import (
	"bufio"
	"os"
	"strings"
)

// ModuleInfo holds the parsed dependency information for a target module.
type ModuleInfo struct {
	Version string // raw version string (semver or pseudo-version)
	Found   bool   // whether the target module was found in go.mod
}

// ParseGoMod reads a go.mod file and extracts the version of targetModule.
// Handles both single-line require and block require syntax.
func ParseGoMod(goModPath, targetModule string) (ModuleInfo, error) {
	f, err := os.Open(goModPath)
	if err != nil {
		return ModuleInfo{}, err
	}
	defer f.Close()

	var inRequireBlock bool
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip comments and empty lines.
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}

		// Detect require block boundaries.
		if strings.HasPrefix(line, "require (") || strings.HasPrefix(line, "require(") {
			inRequireBlock = true
			continue
		}
		if inRequireBlock && line == ")" {
			inRequireBlock = false
			continue
		}

		// Single-line require: require github.com/foo v1.0.0
		if strings.HasPrefix(line, "require ") && !strings.Contains(line, "(") {
			line = strings.TrimPrefix(line, "require ")
			line = strings.TrimSpace(line)
			if _, ver, ok := parseRequireLine(line, targetModule); ok {
				return ModuleInfo{Version: ver, Found: true}, nil
			}
		}

		// Inside require block: each line is "module version"
		if inRequireBlock {
			if _, ver, ok := parseRequireLine(line, targetModule); ok {
				return ModuleInfo{Version: ver, Found: true}, nil
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return ModuleInfo{}, err
	}

	return ModuleInfo{}, nil
}

// parseRequireLine parses a "module version" line and checks if it matches the target.
// Returns the module path, version, and whether it matched.
func parseRequireLine(line, targetModule string) (string, string, bool) {
	// Strip inline comments.
	if idx := strings.Index(line, "//"); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}

	parts := strings.Fields(line)
	if len(parts) < 2 {
		return "", "", false
	}

	mod, ver := parts[0], parts[1]
	if mod == targetModule {
		return mod, ver, true
	}
	return "", "", false
}
