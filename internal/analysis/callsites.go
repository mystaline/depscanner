package analysis

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// CallSite represents a single location where a target function is called.
type CallSite struct {
	File     string // relative path within the repo
	Line     int
	Column   int
	FuncName string // the resolved function name (e.g. "helper.Must")
}

// ScanCallSites walks a repo directory and finds all call sites of funcName
// within packages imported from targetModule.
//
// funcName can be:
//   - A plain name like "Must" — matches in any sub-package of targetModule
//   - A qualified name like "helper.Must" — matches only in that sub-package
//
// Two-pass optimization: first pass uses ImportsOnly to filter files that
// import the target module, second pass does full AST parse only on those files.
//
// The returned warnings list contains diagnostics for files that passed the
// import-only parse but failed full AST parse (likely syntax errors).
func ScanCallSites(repoDir, targetModule, funcName string) ([]CallSite, []string, error) {
	// Parse funcName into optional package qualifier and function name.
	qualPkg, plainFunc := splitQualifiedName(funcName)
	if plainFunc == "" {
		return nil, nil, fmt.Errorf("invalid function name %q: missing function name component", funcName)
	}

	// Pass 1: collect files that import target module + build alias map.
	type fileImport struct {
		path     string
		aliasMap map[string]string // alias/pkg name -> full import path
	}
	var candidates []fileImport
	fset := token.NewFileSet()

	err := WalkGoFiles(repoDir, func(path string) error {
		f, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return nil // skip unparseable
		}

		aliases := make(map[string]string)
		for _, imp := range f.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)

			if importPath == targetModule || strings.HasPrefix(importPath, targetModule+"/") {
				// Derive the canonical package name from the import path.
				parts := strings.Split(importPath, "/")
				pkgBaseName := parts[len(parts)-1]

				// If user specified a package qualifier, match against the import
				// path's base name (not the local alias). Dot imports are always
				// included since they merge into the caller's namespace.
				isDot := imp.Name != nil && imp.Name.Name == "."
				if qualPkg != "" && !isDot && pkgBaseName != qualPkg {
					continue
				}

				// Determine the local name used in code (explicit alias or last path segment).
				var localName string
				if imp.Name != nil {
					localName = imp.Name.Name
				} else {
					localName = pkgBaseName
				}

				aliases[localName] = importPath
			}
		}

		if len(aliases) > 0 {
			candidates = append(candidates, fileImport{path: path, aliasMap: aliases})
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("walk repo: %w", err)
	}

	if len(candidates) == 0 {
		return nil, nil, nil
	}

	// Pass 2: full AST parse on candidate files, find CallExpr matching funcName.
	// Use a fresh FileSet to avoid unbounded offset growth from double-parsing.
	fset2 := token.NewFileSet()
	var sites []CallSite
	var parseWarnings []string

	for _, c := range candidates {
		f, parseErr := parser.ParseFile(fset2, c.path, nil, parser.ParseComments)
		if parseErr != nil {
			// File passed ImportsOnly parse but failed full parse — likely syntax error.
			parseWarnings = append(parseWarnings, fmt.Sprintf("%s: %v", c.path, parseErr))
			continue
		}

		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			resolvedName := matchCallExpr(call, c.aliasMap, plainFunc)
			if resolvedName == "" {
				return true
			}

			pos := fset2.Position(call.Pos())
			relPath, relErr := filepath.Rel(repoDir, pos.Filename)
			if relErr != nil {
				relPath = pos.Filename
			}

			sites = append(sites, CallSite{
				File:     filepath.ToSlash(relPath),
				Line:     pos.Line,
				Column:   pos.Column,
				FuncName: resolvedName,
			})

			return true
		})
	}

	return sites, parseWarnings, nil
}

// matchCallExpr checks if a CallExpr matches the target function via the alias map.
// Returns the resolved "pkg.Func" name or "" if no match.
func matchCallExpr(call *ast.CallExpr, aliasMap map[string]string, funcName string) string {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		// pkg.Func() pattern
		ident, ok := fn.X.(*ast.Ident)
		if !ok {
			return ""
		}
		pkgAlias := ident.Name
		calledFunc := fn.Sel.Name

		if _, isTarget := aliasMap[pkgAlias]; !isTarget {
			return ""
		}
		if calledFunc != funcName {
			return ""
		}
		return pkgAlias + "." + calledFunc

	case *ast.Ident:
		// Direct call (dot-imported). Check if any alias is ".".
		if _, hasDot := aliasMap["."]; !hasDot {
			return ""
		}
		if fn.Name != funcName {
			return ""
		}
		return fn.Name
	}

	return ""
}

// splitQualifiedName splits "helper.Must" into ("helper", "Must")
// and "Must" into ("", "Must").
func splitQualifiedName(name string) (pkg, fn string) {
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[:idx], name[idx+1:]
	}
	return "", name
}
