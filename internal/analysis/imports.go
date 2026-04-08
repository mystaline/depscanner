package analysis

import (
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

// ScanImports walks a repo directory and collects all unique import paths
// that belong to the target module (exact match or sub-packages).
// Returns deduplicated, sorted sub-package paths relative to the target module.
//
//	["helper", "service"]
func ScanImports(repoDir, targetModule string) ([]string, error) {
	seen := make(map[string]struct{})
	fset := token.NewFileSet()

	err := WalkGoFiles(repoDir, func(path string) error {
		f, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return nil // skip unparseable files
		}

		for _, imp := range f.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)

			if importPath == targetModule {
				seen["(root)"] = struct{}{}
				continue
			}

			if subPkg, ok := strings.CutPrefix(importPath, targetModule+"/"); ok {
				// Use only the top-level sub-package name.
				if idx := strings.Index(subPkg, "/"); idx >= 0 {
					subPkg = subPkg[:idx]
				}
				seen[subPkg] = struct{}{}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	pkgs := make([]string, 0, len(seen))
	for pkg := range seen {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)
	return pkgs, nil
}
