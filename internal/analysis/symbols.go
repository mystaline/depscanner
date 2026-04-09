package analysis

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
)

// SymbolKind classifies Go exported symbols.
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

// ParamInfo describes a function/method parameter or return value.
type ParamInfo struct {
	Name string `json:"name,omitempty"`
	Type string `json:"type"`
}

// FieldInfo describes a struct field.
type FieldInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Tag  string `json:"tag,omitempty"`
}

// Symbol represents a single exported Go symbol.
type Symbol struct {
	Kind       SymbolKind  `json:"kind"`
	Name       string      `json:"name"`
	Package    string      `json:"package"`
	Receiver   string      `json:"receiver,omitempty"`
	Params     []ParamInfo `json:"params,omitempty"`
	Returns    []ParamInfo `json:"returns,omitempty"`
	IsVariadic bool        `json:"is_variadic,omitempty"`
	Fields     []FieldInfo `json:"fields,omitempty"`
	Methods    []string    `json:"methods,omitempty"`
	Value      string      `json:"value,omitempty"`
	TypeExpr   string      `json:"type_expr,omitempty"`
}

// SymbolIndex maps qualified symbol names to their definitions.
// Key format: "subpkg.Name" or "subpkg.Receiver.Method"
type SymbolIndex map[string]Symbol

// BuildSymbolIndex parses all Go files in moduleDir and extracts
// every exported symbol into an index.
func BuildSymbolIndex(moduleDir string) (SymbolIndex, error) {
	index := make(SymbolIndex)
	fset := token.NewFileSet()

	err := WalkGoFiles(moduleDir, func(path string) error {
		f, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			return nil // skip unparseable files
		}

		// Derive sub-package name relative to module root.
		relPath, _ := filepath.Rel(moduleDir, path)
		pkg := derivePackage(relPath)

		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				extractFunc(fset, index, pkg, d)
			case *ast.GenDecl:
				extractGenDecl(fset, index, pkg, d)
			}
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("build symbol index: %w", err)
	}
	return index, nil
}

// SortedKeys returns the keys of a SymbolIndex in sorted order.
func (idx SymbolIndex) SortedKeys() []string {
	keys := make([]string, 0, len(idx))
	for k := range idx {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// extractFunc handles function and method declarations.
func extractFunc(fset *token.FileSet, idx SymbolIndex, pkg string, fn *ast.FuncDecl) {
	if !fn.Name.IsExported() {
		return
	}

	sym := Symbol{
		Kind:    KindFunc,
		Name:    fn.Name.Name,
		Package: pkg,
	}

	// Method receiver
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		sym.Kind = KindMethod
		sym.Receiver = typeExprToString(fset, fn.Recv.List[0].Type)
	}

	// Parameters
	if fn.Type.Params != nil {
		sym.Params, sym.IsVariadic = extractFieldList(fset, fn.Type.Params)
	}

	// Returns
	if fn.Type.Results != nil {
		sym.Returns, _ = extractFieldList(fset, fn.Type.Results)
	}

	key := symbolKey(pkg, sym.Receiver, fn.Name.Name)
	idx[key] = sym
}

// extractGenDecl handles type, const, and var declarations.
func extractGenDecl(fset *token.FileSet, idx SymbolIndex, pkg string, gd *ast.GenDecl) {
	for _, spec := range gd.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			extractTypeSpec(fset, idx, pkg, s)
		case *ast.ValueSpec:
			kind := KindVar
			if gd.Tok == token.CONST {
				kind = KindConst
			}
			extractValueSpec(fset, idx, pkg, kind, s)
		}
	}
}

// extractTypeSpec handles struct, interface, and type alias/definition declarations.
func extractTypeSpec(fset *token.FileSet, idx SymbolIndex, pkg string, ts *ast.TypeSpec) {
	if !ts.Name.IsExported() {
		return
	}

	switch t := ts.Type.(type) {
	case *ast.StructType:
		sym := Symbol{
			Kind:    KindStruct,
			Name:    ts.Name.Name,
			Package: pkg,
		}
		if t.Fields != nil {
			for _, field := range t.Fields.List {
				fieldType := typeExprToString(fset, field.Type)
				tag := ""
				if field.Tag != nil {
					tag = field.Tag.Value
				}

				if len(field.Names) == 0 {
					// Embedded field
					sym.Fields = append(sym.Fields, FieldInfo{
						Name: fieldType,
						Type: fieldType,
						Tag:  tag,
					})
				} else {
					for _, name := range field.Names {
						if name.IsExported() {
							sym.Fields = append(sym.Fields, FieldInfo{
								Name: name.Name,
								Type: fieldType,
								Tag:  tag,
							})
						}
					}
				}
			}
		}
		idx[symbolKey(pkg, "", ts.Name.Name)] = sym

	case *ast.InterfaceType:
		sym := Symbol{
			Kind:    KindInterface,
			Name:    ts.Name.Name,
			Package: pkg,
		}
		if t.Methods != nil {
			for _, m := range t.Methods.List {
				sig := typeExprToString(fset, m.Type)
				for _, name := range m.Names {
					sym.Methods = append(sym.Methods, name.Name+sig)
				}
				// Embedded interface
				if len(m.Names) == 0 {
					sym.Methods = append(sym.Methods, sig)
				}
			}
		}
		sort.Strings(sym.Methods)
		idx[symbolKey(pkg, "", ts.Name.Name)] = sym

	default:
		// Type alias or type definition
		sym := Symbol{
			Kind:     KindType,
			Name:     ts.Name.Name,
			Package:  pkg,
			TypeExpr: typeExprToString(fset, ts.Type),
		}
		idx[symbolKey(pkg, "", ts.Name.Name)] = sym
	}
}

// extractValueSpec handles const and var declarations.
func extractValueSpec(fset *token.FileSet, idx SymbolIndex, pkg string, kind SymbolKind, vs *ast.ValueSpec) {
	for i, name := range vs.Names {
		if !name.IsExported() {
			continue
		}

		sym := Symbol{
			Kind:    kind,
			Name:    name.Name,
			Package: pkg,
		}

		if vs.Type != nil {
			sym.TypeExpr = typeExprToString(fset, vs.Type)
		}

		if i < len(vs.Values) {
			sym.Value = exprToString(fset, vs.Values[i])
		}

		idx[symbolKey(pkg, "", name.Name)] = sym
	}
}

// extractFieldList converts an ast.FieldList into []ParamInfo and detects variadic.
func extractFieldList(fset *token.FileSet, fl *ast.FieldList) ([]ParamInfo, bool) {
	var params []ParamInfo
	isVariadic := false

	for i, field := range fl.List {
		typStr := typeExprToString(fset, field.Type)

		// Variadic detection on the last parameter
		if i == len(fl.List)-1 {
			if _, ok := field.Type.(*ast.Ellipsis); ok {
				isVariadic = true
			}
		}

		if len(field.Names) == 0 {
			// Unnamed parameter (e.g. func Foo(int, string))
			params = append(params, ParamInfo{Type: typStr})
		} else {
			for _, name := range field.Names {
				params = append(params, ParamInfo{Name: name.Name, Type: typStr})
			}
		}
	}
	return params, isVariadic
}

// typeExprToString uses go/printer for canonical type representation.
func typeExprToString(fset *token.FileSet, expr ast.Expr) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, expr); err != nil {
		return fmt.Sprintf("%T", expr)
	}
	return buf.String()
}

// exprToString renders any expression to string using go/printer.
func exprToString(fset *token.FileSet, expr ast.Expr) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, expr); err != nil {
		return "?"
	}
	return buf.String()
}

// symbolKey builds the unique key for a symbol in the index.
func symbolKey(pkg, receiver, name string) string {
	if receiver != "" {
		// Strip pointer prefix for consistent keying
		recv := strings.TrimPrefix(receiver, "*")
		if pkg != "" {
			return pkg + "." + recv + "." + name
		}
		return recv + "." + name
	}
	if pkg != "" {
		return pkg + "." + name
	}
	return name
}

// derivePackage extracts the sub-package directory from a relative file path.
func derivePackage(relPath string) string {
	dir := filepath.Dir(relPath)
	dir = filepath.ToSlash(dir)
	if dir == "." {
		return ""
	}
	return dir
}
