package analysis

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// TypeRef represents a usage site of a target module type in consumer code.
type TypeRef struct {
	File     string
	Line     int
	TypeName string // fully qualified, e.g. "gitea.example.com/org/lib/service.SchedulerService"
	RawName  string // as written in source, e.g. "service.SchedulerService"
	Context  string // where the type appears: "field", "var", "param", "return", "assert", "composite"
}

// ScanTypeReferences walks a consumer repo and finds every declaration or usage
// site that references a specific type from the target module.
//
// typeName can be a plain name like "SchedulerService" (matches any sub-package)
// or qualified like "service.SchedulerService" (matches only that sub-package).
func ScanTypeReferences(repoDir, targetModule, typeName string) ([]TypeRef, error) {
	qualPkg, plainType := splitQualifiedName(typeName)
	if plainType == "" {
		return nil, fmt.Errorf("invalid type name %q", typeName)
	}

	type fileImport struct {
		path     string
		aliasMap map[string]string
	}
	var candidates []fileImport
	fset := token.NewFileSet()

	err := WalkGoFiles(repoDir, func(path string) error {
		f, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return nil
		}

		aliases := make(map[string]string)
		for _, imp := range f.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if importPath == targetModule || strings.HasPrefix(importPath, targetModule+"/") {
				parts := strings.Split(importPath, "/")
				pkgBaseName := parts[len(parts)-1]

				if qualPkg != "" {
					relPath := ""
					if importPath != targetModule {
						relPath = strings.TrimPrefix(importPath, targetModule+"/")
					}
					if relPath != qualPkg && pkgBaseName != qualPkg {
						continue
					}
				}

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
		return nil, fmt.Errorf("walk repo: %w", err)
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	fset2 := token.NewFileSet()
	var refs []TypeRef

	for _, c := range candidates {
		f, parseErr := parser.ParseFile(fset2, c.path, nil, parser.ParseComments)
		if parseErr != nil {
			continue
		}

		ast.Inspect(f, func(n ast.Node) bool {
			switch node := n.(type) {

			case *ast.Field:
				refs = checkTypeExpr(refs, fset2, repoDir, node.Type, c.aliasMap, plainType, "field")
				refs = checkDotImportRef(refs, fset2, repoDir, node.Type, c.aliasMap, plainType, "field")

			case *ast.ValueSpec:
				refs = checkTypeExpr(refs, fset2, repoDir, node.Type, c.aliasMap, plainType, "var")
				refs = checkDotImportRef(refs, fset2, repoDir, node.Type, c.aliasMap, plainType, "var")
				for _, val := range node.Values {
					if comp, ok := val.(*ast.CompositeLit); ok {
						refs = checkTypeExpr(refs, fset2, repoDir, comp.Type, c.aliasMap, plainType, "composite")
						refs = checkDotImportRef(refs, fset2, repoDir, comp.Type, c.aliasMap, plainType, "composite")
					}
				}

			case *ast.TypeAssertExpr:
				refs = checkTypeExpr(refs, fset2, repoDir, node.Type, c.aliasMap, plainType, "assert")
				refs = checkDotImportRef(refs, fset2, repoDir, node.Type, c.aliasMap, plainType, "assert")

			case *ast.CompositeLit:
				refs = checkTypeExpr(refs, fset2, repoDir, node.Type, c.aliasMap, plainType, "composite")
				refs = checkDotImportRef(refs, fset2, repoDir, node.Type, c.aliasMap, plainType, "composite")

			case *ast.FuncDecl:
				// Receiver
				if node.Recv != nil {
					for _, field := range node.Recv.List {
						refs = checkTypeExpr(refs, fset2, repoDir, field.Type, c.aliasMap, plainType, "receiver")
						refs = checkDotImportRef(refs, fset2, repoDir, field.Type, c.aliasMap, plainType, "receiver")
					}
				}
				if node.Type.Params != nil {
					for _, field := range node.Type.Params.List {
						refs = checkTypeExpr(refs, fset2, repoDir, field.Type, c.aliasMap, plainType, "param")
						refs = checkDotImportRef(refs, fset2, repoDir, field.Type, c.aliasMap, plainType, "param")
					}
				}
				if node.Type.Results != nil {
					for _, field := range node.Type.Results.List {
						refs = checkTypeExpr(refs, fset2, repoDir, field.Type, c.aliasMap, plainType, "return")
						refs = checkDotImportRef(refs, fset2, repoDir, field.Type, c.aliasMap, plainType, "return")
					}
				}
			}
			return true
		})
	}

	return refs, nil
}

func checkTypeExpr(refs []TypeRef, fset *token.FileSet, repoDir string, expr ast.Expr, aliasMap map[string]string, typeName, context string) []TypeRef {
	if expr == nil {
		return refs
	}

	// Unwrap pointer, array, slice, map, chan types.
	expr = unwrapTypeExpr(expr)

	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return refs
	}
	if sel.Sel.Name != typeName {
		return refs
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return refs
	}

	importPath, isTarget := aliasMap[ident.Name]
	if !isTarget {
		return refs
	}

	pos := fset.Position(sel.Pos())
	relPath, _ := filepath.Rel(repoDir, pos.Filename)
	relPath = filepath.ToSlash(relPath)

	return append(refs, TypeRef{
		File:     relPath,
		Line:     pos.Line,
		TypeName: importPath + "." + typeName,
		RawName:  ident.Name + "." + typeName,
		Context:  context,
	})
}

// unwrapTypeExpr strips wrapping type expressions (pointer, array, slice, map, chan)
// to reach the inner SelectorExpr. Returns the original expression if no wrapping found.
func unwrapTypeExpr(expr ast.Expr) ast.Expr {
	for {
		switch e := expr.(type) {
		case *ast.StarExpr:
			expr = e.X
		case *ast.ArrayType:
			expr = e.Elt
		case *ast.MapType:
			expr = e.Value
		case *ast.ChanType:
			expr = e.Value
		default:
			return expr
		}
	}
}

// checkDotImportRef handles type references via dot imports (import . "pkg").
// When dot-imported, types appear as bare idents, not as pkg.Type.
func checkDotImportRef(refs []TypeRef, fset *token.FileSet, repoDir string, expr ast.Expr, aliasMap map[string]string, typeName, context string) []TypeRef {
	importPath, hasDot := aliasMap["."]
	if !hasDot {
		return refs
	}

	// Unwrap wrapper types to reach the underlying ident
	inner := unwrapTypeExpr(expr)

	// Check for SelectorExpr with dot import — unlikely since dot imports don't use selector
	if sel, ok := inner.(*ast.SelectorExpr); ok {
		if sel.Sel.Name == typeName {
			if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "." {
				pos := fset.Position(sel.Pos())
				relPath, _ := filepath.Rel(repoDir, pos.Filename)
				relPath = filepath.ToSlash(relPath)
				return append(refs, TypeRef{
					File:     relPath,
					Line:     pos.Line,
					TypeName: importPath + "." + typeName,
					RawName:  "." + typeName,
					Context:  context,
				})
			}
		}
	}

	// Check for bare Ident used directly via dot import: TypeName
	if id, ok := inner.(*ast.Ident); ok && id.Name == typeName {
		pos := fset.Position(id.Pos())
		relPath, _ := filepath.Rel(repoDir, pos.Filename)
		relPath = filepath.ToSlash(relPath)
		return append(refs, TypeRef{
			File:     relPath,
			Line:     pos.Line,
			TypeName: importPath + "." + typeName,
			RawName:  typeName,
			Context:  context,
		})
	}

	// Check for CompositeLit with dot import: TypeName{...}
	if comp, ok := expr.(*ast.CompositeLit); ok {
		if id, ok := comp.Type.(*ast.Ident); ok && id.Name == typeName {
			pos := fset.Position(id.Pos())
			relPath, _ := filepath.Rel(repoDir, pos.Filename)
			relPath = filepath.ToSlash(relPath)
			return append(refs, TypeRef{
				File:     relPath,
				Line:     pos.Line,
				TypeName: importPath + "." + typeName,
				RawName:  typeName,
				Context:  context,
			})
		}
	}

	return refs
}
