// Package analysis provides tools for analyzing Go source code, including
// function signature extraction and call-site matching.
package analysis

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// FuncSignature represents the parameter structure of a Go function.
// This is used for legacy validation (argument count checks).
type FuncSignature struct {
	Name        string // Full name including package
	ParamsCount int    // Number of positional parameters
	IsVariadic  bool   // True if the last parameter is ...args
}

// ParseSignature searches the moduleDir for a function called funcName
// and extracts its signature from the AST.
//
// Logic Flow:
// 1. Traverse all Go files in the module.
// 2. Parse each file and use ast.Inspect to find FuncDecl nodes.
// 3. Match the function name and count the parameters (resolving identifiers).
// 4. Return the first match found.
func ParseSignature(moduleDir, funcName string) (*FuncSignature, error) {
	qualPkg, plainFunc := splitQualifiedName(funcName)
	fset := token.NewFileSet()
	var found *FuncSignature

	err := WalkGoFiles(moduleDir, func(path string) error {
		if found != nil {
			return nil
		}

		// Optimization: if we have a package qualifier, check if file is likely in that pkg.
		if qualPkg != "" {
			relPath, _ := filepath.Rel(moduleDir, path)
			relPkg := filepathToPkg(relPath)
			if relPkg != "" && !strings.HasPrefix(relPkg, qualPkg) && !strings.HasPrefix(qualPkg, relPkg) {
				return nil
			}
		}

		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse file %s: %w", path, err)
		}

		ast.Inspect(f, func(n ast.Node) bool {
			if found != nil {
				return false
			}

			fn, ok := n.(*ast.FuncDecl)
			if !ok {
				return true
			}

			if fn.Name.Name == plainFunc {
				count := 0
				isVariadic := false
				for i, field := range fn.Type.Params.List {
					count += len(field.Names)
					if i == len(fn.Type.Params.List)-1 {
						if _, ok := field.Type.(*ast.Ellipsis); ok {
							isVariadic = true
						}
					}
				}

				found = &FuncSignature{
					Name:        funcName,
					ParamsCount: count,
					IsVariadic:  isVariadic,
				}
				return false
			}
			return true
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, fmt.Errorf("function %q not found in %s", funcName, moduleDir)
	}
	return found, nil
}

// filepathToPkg converts a file path like "pkg/sub/file.go" into a package identity "pkg.sub".
func filepathToPkg(path string) string {
	dir := strings.ReplaceAll(path, "\\", "/")
	idx := strings.LastIndex(dir, "/")
	if idx == -1 {
		return ""
	}
	return strings.ReplaceAll(dir[:idx], "/", ".")
}
