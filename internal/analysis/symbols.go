package analysis

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
)

// SymbolKind classifies Go symbols.
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

// Symbol represents a Go symbol (exported or internal).
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
	IsExported bool        `json:"is_exported"`

	// Behavioral Audit Fields (Hybrid Approach)
	BodyHash    string   `json:"body_hash,omitempty"`    // SHA256 of the symbol's implementation
	UsedSymbols []string `json:"used_symbols,omitempty"` // Names of other symbols referenced in body
}

// SymbolIndex maps qualified symbol names to their definitions.
type SymbolIndex map[string]Symbol

// BuildSymbolIndex parses all Go files in moduleDir and extracts
// symbols into an index. It collects BOTH exported and internal symbols
// to enable behavioral impact analysis.
func BuildSymbolIndex(moduleDir string) (SymbolIndex, error) {
	index := make(SymbolIndex)
	fset := token.NewFileSet()

	err := WalkGoFiles(moduleDir, func(path string) error {
		f, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			return nil // skip unparseable files
		}

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
	sym := Symbol{
		Kind:       KindFunc,
		Name:       fn.Name.Name,
		Package:    pkg,
		IsExported: fn.Name.IsExported(),
	}

	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		sym.Kind = KindMethod
		sym.Receiver = typeExprToString(fset, fn.Recv.List[0].Type)
		// Receiver type (e.g. *T) doesn't decide export status, the method name does.
	}

	if fn.Type.Params != nil {
		sym.Params, sym.IsVariadic = extractFieldList(fset, fn.Type.Params)
	}
	if fn.Type.Results != nil {
		sym.Returns, _ = extractFieldList(fset, fn.Type.Results)
	}

	// Behavioral Analysis: Hash the body and collect referenced symbols.
	if fn.Body != nil {
		sym.BodyHash = hashNode(fset, fn.Body)
		sym.UsedSymbols = collectUsedSymbols(fn.Body)
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
	exported := ts.Name.IsExported()

	switch t := ts.Type.(type) {
	case *ast.StructType:
		sym := Symbol{
			Kind:       KindStruct,
			Name:       ts.Name.Name,
			Package:    pkg,
			IsExported: exported,
		}
		if t.Fields != nil {
			for _, field := range t.Fields.List {
				fieldType := typeExprToString(fset, field.Type)
				tag := ""
				if field.Tag != nil {
					tag = field.Tag.Value
				}

				if len(field.Names) == 0 {
					sym.Fields = append(sym.Fields, FieldInfo{
						Name: fieldType,
						Type: fieldType,
						Tag:  tag,
					})
				} else {
					for _, name := range field.Names {
						// We only record exported fields as part of the public API,
						// but the struct itself could be internal.
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
			Kind:       KindInterface,
			Name:       ts.Name.Name,
			Package:    pkg,
			IsExported: exported,
		}
		if t.Methods != nil {
			for _, m := range t.Methods.List {
				sig := typeExprToString(fset, m.Type)
				for _, name := range m.Names {
					sym.Methods = append(sym.Methods, name.Name+sig)
				}
				if len(m.Names) == 0 {
					sym.Methods = append(sym.Methods, sig)
				}
			}
		}
		sort.Strings(sym.Methods)
		idx[symbolKey(pkg, "", ts.Name.Name)] = sym

	default:
		sym := Symbol{
			Kind:       KindType,
			Name:       ts.Name.Name,
			Package:    pkg,
			TypeExpr:   typeExprToString(fset, ts.Type),
			IsExported: exported,
		}
		idx[symbolKey(pkg, "", ts.Name.Name)] = sym
	}
}

// extractValueSpec handles const and var declarations.
func extractValueSpec(fset *token.FileSet, idx SymbolIndex, pkg string, kind SymbolKind, vs *ast.ValueSpec) {
	for i, name := range vs.Names {
		sym := Symbol{
			Kind:       kind,
			Name:       name.Name,
			Package:    pkg,
			IsExported: name.IsExported(),
		}
		if vs.Type != nil {
			sym.TypeExpr = typeExprToString(fset, vs.Type)
		}
		if i < len(vs.Values) {
			sym.Value = exprToString(fset, vs.Values[i])
			// Track if constant value depends on other symbols (simplified)
			sym.UsedSymbols = collectUsedSymbols(vs.Values[i])
		}
		idx[symbolKey(pkg, "", name.Name)] = sym
	}
}

// hashNode returns a SHA256 hash of the canonical string representation of an AST node.
func hashNode(fset *token.FileSet, node ast.Node) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, node); err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(buf.Bytes()))
}

// collectUsedSymbols scans a node for all identifiers (potential symbol names).
func collectUsedSymbols(node ast.Node) []string {
	ids := make(map[string]struct{})
	ast.Inspect(node, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			// Basic name call (internal to package)
			if x.Name != "" && !isBuiltin(x.Name) {
				ids[x.Name] = struct{}{}
			}
		case *ast.SelectorExpr:
			// pkg.Func() call
			if x.Sel != nil && x.Sel.Name != "" {
				// We still collect the name. The propagation logic will try to resolve it.
				ids[x.Sel.Name] = struct{}{}
			}
		}
		return true
	})

	var result []string
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func isBuiltin(name string) bool {
	switch name {
	case "nil", "true", "false", "err", "len", "cap", "append", "make", "new", "panic", "recover", "print", "println":
		return true
	}
	return false
}

// extractFieldList converts an ast.FieldList into []ParamInfo and detects variadic.
func extractFieldList(fset *token.FileSet, fl *ast.FieldList) ([]ParamInfo, bool) {
	var params []ParamInfo
	isVariadic := false
	for i, field := range fl.List {
		typStr := typeExprToString(fset, field.Type)
		if i == len(fl.List)-1 {
			if _, ok := field.Type.(*ast.Ellipsis); ok {
				isVariadic = true
			}
		}
		if len(field.Names) == 0 {
			params = append(params, ParamInfo{Type: typStr})
		} else {
			for _, name := range field.Names {
				params = append(params, ParamInfo{Name: name.Name, Type: typStr})
			}
		}
	}
	return params, isVariadic
}

func typeExprToString(fset *token.FileSet, expr ast.Expr) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, expr); err != nil {
		return fmt.Sprintf("%T", expr)
	}
	return buf.String()
}

func exprToString(fset *token.FileSet, expr ast.Expr) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, expr); err != nil {
		return "?"
	}
	return buf.String()
}

func symbolKey(pkg, receiver, name string) string {
	if receiver != "" {
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

func derivePackage(relPath string) string {
	dir := filepath.Dir(relPath)
	dir = filepath.ToSlash(dir)
	if dir == "." {
		return ""
	}
	return dir
}
