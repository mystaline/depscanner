package analysis

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"strings"
	"bytes"
	"crypto/sha256"
)

// SymbolKind represents the type of symbols (Func, Struct, etc.)
type SymbolKind string

const (
	KindFunc      SymbolKind = "func"
	KindMethod    SymbolKind = "method"
	KindStruct    SymbolKind = "struct"
	KindInterface SymbolKind = "interface"
	KindConst     SymbolKind = "const"
	KindVar       SymbolKind = "var"
	KindType      SymbolKind = "type"
)

// Symbol represents a single code entity in the target module.
type Symbol struct {
	Kind        SymbolKind  `json:"kind"`
	Name        string      `json:"name"`
	Package     string      `json:"package"`
	File        string      `json:"file,omitempty"`
	Receiver    string      `json:"receiver,omitempty"`
	Params      []ParamInfo `json:"params,omitempty"`
	Returns     []ParamInfo `json:"returns,omitempty"`
	IsVariadic  bool        `json:"is_variadic,omitempty"`
	Fields      []FieldInfo `json:"fields,omitempty"`
	Methods     []string    `json:"methods,omitempty"`
	Value       string      `json:"value,omitempty"`
	TypeExpr    string      `json:"type_expr,omitempty"` 
	IsExported  bool        `json:"is_exported"`
	BodyHash    string      `json:"body_hash,omitempty"`
	UsedSymbols []string    `json:"used_symbols,omitempty"`
	StartLine   int         `json:"start_line,omitempty"`
	EndLine     int         `json:"end_line,omitempty"`
}

// ParamInfo holds type information for function parameters or returns.
type ParamInfo struct {
	Name string `json:"name,omitempty"`
	Type string `json:"type"`
}

// FieldInfo holds information for struct fields.
type FieldInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Tag  string `json:"tag,omitempty"`
}

// QualifiedName returns the full "package.Receiver.Name" or "package.Name".
func (s Symbol) QualifiedName() string {
	if s.Receiver != "" {
		return fmt.Sprintf("%s.%s.%s", s.Package, s.Receiver, s.Name)
	}
	return fmt.Sprintf("%s.%s", s.Package, s.Name)
}

// SymbolIndex is a map from QualifiedName to Symbol.
type SymbolIndex map[string]Symbol

// BuildSymbolIndex scans a directory for Go files and indexes all exported symbols.
func BuildSymbolIndex(moduleDir string) (SymbolIndex, error) {
	idx := make(SymbolIndex)
	fset := token.NewFileSet()

	err := WalkGoFiles(moduleDir, func(path string) error {
		f, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			return parseErr
		}

		pkgPath := getPackagePath(moduleDir, path, f.Name.Name)

		ast.Inspect(f, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.FuncDecl:
				if node.Name.IsExported() {
					extractFunc(fset, idx, pkgPath, node)
				}
			case *ast.GenDecl:
				extractGenDecl(fset, idx, pkgPath, node)
			}
			return true
		})
		return nil
	})

	return idx, err
}

func getPackagePath(moduleDir, filePath, pkgName string) string {
	rel, _ := filepath.Rel(moduleDir, filePath)
	dir := filepath.Dir(rel)
	if dir == "." {
		return pkgName
	}
	return filepath.ToSlash(dir)
}

func extractFunc(fset *token.FileSet, idx SymbolIndex, pkg string, fn *ast.FuncDecl) {
	sym := Symbol{
		Kind:       KindFunc,
		Name:       fn.Name.Name,
		Package:    pkg,
		File:       fset.Position(fn.Pos()).Filename,
		IsExported: fn.Name.IsExported(),
		StartLine:  fset.Position(fn.Pos()).Line,
		EndLine:    fset.Position(fn.End()).Line,
	}

	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		sym.Kind = KindMethod
		sym.Receiver = typeExprToString(fset, fn.Recv.List[0].Type)
		sym.Receiver = strings.TrimPrefix(sym.Receiver, "*")
	}

	sym.Params, sym.IsVariadic = extractFieldList(fset, fn.Type.Params)
	sym.Returns, _ = extractFieldList(fset, fn.Type.Results)

	if fn.Body != nil {
		sym.BodyHash = hashNode(fset, fn.Body)
		sym.UsedSymbols = collectUsedSymbols(fset, fn.Body)
	}

	idx[sym.QualifiedName()] = sym
}

func extractGenDecl(fset *token.FileSet, idx SymbolIndex, pkg string, gd *ast.GenDecl) {
	var kind SymbolKind
	switch gd.Tok {
	case token.CONST:
		kind = KindConst
	case token.VAR:
		kind = KindVar
	case token.TYPE:
		// handled in TypeSpec
	default:
		return
	}

	for _, spec := range gd.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			extractTypeSpec(fset, idx, pkg, s)
		case *ast.ValueSpec:
			if kind != "" {
				extractValueSpec(fset, idx, pkg, kind, s)
			}
		}
	}
}

func extractTypeSpec(fset *token.FileSet, idx SymbolIndex, pkg string, ts *ast.TypeSpec) {
	exported := ts.Name.IsExported()
	if !exported {
		return
	}

	switch t := ts.Type.(type) {
	case *ast.StructType:
		sym := Symbol{
			Kind:       KindStruct,
			Name:       ts.Name.Name,
			Package:    pkg,
			File:       fset.Position(ts.Pos()).Filename,
			IsExported: exported,
			StartLine:  fset.Position(ts.Pos()).Line,
			EndLine:    fset.Position(ts.End()).Line,
		}
		for _, field := range t.Fields.List {
			for _, name := range field.Names {
				finfo := FieldInfo{
					Name: name.Name,
					Type: typeExprToString(fset, field.Type),
				}
				if field.Tag != nil {
					finfo.Tag = strings.Trim(field.Tag.Value, "`")
				}
				sym.Fields = append(sym.Fields, finfo)
			}
		}
		idx[sym.QualifiedName()] = sym

	case *ast.InterfaceType:
		sym := Symbol{
			Kind:       KindInterface,
			Name:       ts.Name.Name,
			Package:    pkg,
			File:       fset.Position(ts.Pos()).Filename,
			IsExported: exported,
			StartLine:  fset.Position(ts.Pos()).Line,
			EndLine:    fset.Position(ts.End()).Line,
		}
		for _, method := range t.Methods.List {
			if len(method.Names) > 0 {
				sym.Methods = append(sym.Methods, method.Names[0].Name)
			}
		}
		idx[sym.QualifiedName()] = sym

	default:
		sym := Symbol{
			Kind:       KindType,
			Name:       ts.Name.Name,
			Package:    pkg,
			File:       fset.Position(ts.Pos()).Filename,
			IsExported: exported,
			Value:      typeExprToString(fset, t),
			TypeExpr:   typeExprToString(fset, t),
			StartLine:  fset.Position(ts.Pos()).Line,
			EndLine:    fset.Position(ts.End()).Line,
		}
		idx[sym.QualifiedName()] = sym
	}
}

func extractValueSpec(fset *token.FileSet, idx SymbolIndex, pkg string, kind SymbolKind, vs *ast.ValueSpec) {
	for i, name := range vs.Names {
		if !name.IsExported() {
			continue
		}
		sym := Symbol{
			Kind:       kind,
			Name:       name.Name,
			Package:    pkg,
			File:       fset.Position(name.Pos()).Filename,
			IsExported: true,
			StartLine:  fset.Position(vs.Pos()).Line,
			EndLine:    fset.Position(vs.End()).Line,
		}
		if vs.Type != nil {
			sym.Value = typeExprToString(fset, vs.Type)
			sym.TypeExpr = typeExprToString(fset, vs.Type)
		} else if i < len(vs.Values) {
			sym.Value = exprToString(fset, vs.Values[i])
		}
		idx[sym.QualifiedName()] = sym
	}
}

func typeExprToString(fset *token.FileSet, expr ast.Expr) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, expr); err != nil {
		return fmt.Sprintf("%T", expr)
	}
	return buf.String()
}

func exprToString(fset *token.FileSet, expr ast.Expr) string {
	return typeExprToString(fset, expr)
}

func hashNode(fset *token.FileSet, node ast.Node) string {
	if node == nil {
		return ""
	}
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, node); err != nil {
		return ""
	}
	h := sha256.New()
	h.Write(buf.Bytes())
	return fmt.Sprintf("%x", h.Sum(nil))
}

func collectUsedSymbols(fset *token.FileSet, root ast.Node) []string {
	var symbols []string
	ast.Inspect(root, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			if x, ok := node.X.(*ast.Ident); ok {
				symbols = append(symbols, x.Name+"."+node.Sel.Name)
			}
		case *ast.Ident:
			symbols = append(symbols, node.Name)
		}
		return true
	})
	return symbols
}

func extractFieldList(fset *token.FileSet, fl *ast.FieldList) ([]ParamInfo, bool) {
	if fl == nil {
		return nil, false
	}
	var params []ParamInfo
	variadic := false
	for _, field := range fl.List {
		typ := typeExprToString(fset, field.Type)
		if _, ok := field.Type.(*ast.Ellipsis); ok {
			variadic = true
		}
		if len(field.Names) == 0 {
			params = append(params, ParamInfo{Type: typ})
		} else {
			for _, name := range field.Names {
				params = append(params, ParamInfo{Name: name.Name, Type: typ})
			}
		}
	}
	return params, variadic
}
