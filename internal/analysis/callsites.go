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
	FuncName string // the resolved function name (e.g. "github.com/org/lib/helper.Must")
	RawName  string // the original name in code (e.g. "helper.Must")
	ArgCount int    // number of arguments in the call
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

				// If user specified a package qualifier, match against the relative
				// part of the import path. Dot imports are always
				// included since they merge into the caller's namespace.
				isDot := imp.Name != nil && imp.Name.Name == "."
				if qualPkg != "" && !isDot {
					relPath := ""
					if importPath != targetModule {
						relPath = strings.TrimPrefix(importPath, targetModule+"/")
					}
					// Method support: qualPkg might be "pkg.Receiver", so we check prefix
					if relPath != qualPkg && pkgBaseName != qualPkg && !strings.HasPrefix(qualPkg, relPath+".") {
						continue
					}
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
			parseWarnings = append(parseWarnings, fmt.Sprintf("%s: %v", c.path, parseErr))
			continue
		}

		var walk func(n ast.Node, insideSelector bool)
		walk = func(n ast.Node, insideSelector bool) {
			if n == nil {
				return
			}

			switch node := n.(type) {
			case *ast.SelectorExpr:
				resolvedName, rawName := matchSelectorExpr(node, c.aliasMap, plainFunc)
				if resolvedName != "" {
					addSite(fset2, &sites, node, repoDir, resolvedName, rawName)
					return // matched, stop descending
				}
				// Descend into X (might be a package or another object)
				walk(node.X, false)
				// Sel is inside a selector, mark it so dot-import logic skips it
				walk(node.Sel, true)
				return

			case *ast.Ident:
				if !insideSelector {
					resolvedName, rawName := matchIdent(node, c.aliasMap, plainFunc)
					if resolvedName != "" {
						addSite(fset2, &sites, node, repoDir, resolvedName, rawName)
					}
				}
				return
			}

			// Generic descent for all other nodes
			ast.Inspect(n, func(child ast.Node) bool {
				if child == nil || child == n {
					return true
				}
				walk(child, false)
				return false
			})
		}

		walk(f, false)
	}

	return sites, parseWarnings, nil
}

func addSite(fset *token.FileSet, sites *[]CallSite, n ast.Node, repoDir, resolved, raw string) {
	pos := fset.Position(n.Pos())
	relPath, relErr := filepath.Rel(repoDir, pos.Filename)
	if relErr != nil {
		relPath = pos.Filename
	}

	// Determine arg count if it's a call
	argCount := 0
	// We check if the parent or part of the context is a call,
	// but for now, we just report 0 for non-calls.

	*sites = append(*sites, CallSite{
		File:     filepath.ToSlash(relPath),
		Line:     pos.Line,
		Column:   pos.Column,
		FuncName: resolved,
		RawName:  raw,
		ArgCount: argCount,
	})
}

// matchSelectorExpr checks if a SelectorExpr matches the target symbol.
func matchSelectorExpr(sel *ast.SelectorExpr, aliasMap map[string]string, symbolName string) (string, string) {
	siteFuncName := sel.Sel.Name

	// Handle complex symbols like "Receiver.Method"
	targetReceiver, targetName := splitSymbolKey(symbolName)

	if targetName != "" {
		// It's a method/field with a receiver
		if siteFuncName != targetName {
			return "", ""
		}
	} else {
		// Plain function or symbol
		if siteFuncName != symbolName {
			return "", ""
		}
	}

	// The X part must be an Ident (the package alias or object name)
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", ""
	}
	pkgAlias := ident.Name

	importPath, isTarget := aliasMap[pkgAlias]
	if isTarget {
		return importPath + "." + siteFuncName, pkgAlias + "." + siteFuncName
	}

	// Heuristic for method calls on objects
	if targetReceiver != "" && pkgAlias != "" {
		// If we find "obj.Method" and we are looking for "Receiver.Method",
		// we match by name since we don't have type info.
		return pkgAlias + "." + siteFuncName, pkgAlias + "." + siteFuncName
	}

	return "", ""
}

// matchIdent checks if an Ident matches the target symbol (dot-imports).
func matchIdent(id *ast.Ident, aliasMap map[string]string, symbolName string) (string, string) {
	if id.Name != symbolName {
		return "", ""
	}

	importPath, hasDot := aliasMap["."]
	if !hasDot {
		return "", ""
	}
	return importPath + "." + id.Name, id.Name
}

// splitQualifiedName splits "helper.Must" into ("helper", "Must")
// and "Must" into ("", "Must").
func splitQualifiedName(name string) (pkg, fn string) {
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[:idx], name[idx+1:]
	}
	return "", name
}
