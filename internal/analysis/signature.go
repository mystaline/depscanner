package analysis

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// FuncSignature represents the parameter signature of a Go function.
type FuncSignature struct {
	Name        string
	ParamsCount int
	IsVariadic  bool
}

// ParseSignature searches the target module directory for a function definition
// matching funcName and returns its parameter signature.
func ParseSignature(moduleDir, funcName string) (*FuncSignature, error) {
	qualPkg, plainFunc := splitQualifiedName(funcName)
	fset := token.NewFileSet()
	var found *FuncSignature

	err := WalkGoFiles(moduleDir, func(path string) error {
		if found != nil {
			return nil
		}

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

func filepathToPkg(path string) string {
	dir := strings.ReplaceAll(path, "\\", "/")
	idx := strings.LastIndex(dir, "/")
	if idx == -1 {
		return ""
	}
	return strings.ReplaceAll(dir[:idx], "/", ".")
}
