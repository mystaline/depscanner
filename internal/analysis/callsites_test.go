package analysis

import (
	"go/ast"
	"go/parser"
	"os"
	"path/filepath"
	"testing"
)

func TestMatchCallExpr(t *testing.T) {
	aliasMap := map[string]string{
		"util":   "github.com/org/lib/util",
		"db":     "github.com/org/lib/db",
		"config": "github.com/org/lib/config",
		".":      "github.com/org/lib/dot",
	}

	tests := []struct {
		name         string
		expr         string
		funcName     string
		wantResolved string
		wantRaw      string
	}{
		{
			name:         "package alias match",
			funcName:     "Process",
			expr:         "util.Process()",
			wantResolved: "github.com/org/lib/util.Process",
			wantRaw:      "util.Process",
		},
		{
			name:         "dot import match",
			funcName:     "DoSomething",
			expr:         "DoSomething()",
			wantResolved: "github.com/org/lib/dot.DoSomething",
			wantRaw:      "DoSomething",
		},
		{
			name:         "mismatch function name",
			funcName:     "Other",
			expr:         "util.Process()",
			wantResolved: "",
			wantRaw:      "",
		},
		{
			name:         "const reference match",
			funcName:     "MAX_RETRIES",
			expr:         "config.MAX_RETRIES",
			wantResolved: "github.com/org/lib/config.MAX_RETRIES",
			wantRaw:      "config.MAX_RETRIES",
		},

		{
			name:         "composite literal usage",
			funcName:     "Config",
			expr:         "&config.Config{}",
			wantResolved: "github.com/org/lib/config.Config",
			wantRaw:      "config.Config",
		},
		{
			name:         "type conversion or cast",
			funcName:     "MyType",
			expr:         "config.MyType(123)",
			wantResolved: "github.com/org/lib/config.MyType",
			wantRaw:      "config.MyType",
		},
		{
			name:         "shadowed variable (different alias)",
			funcName:     "Conflict",
			expr:         "myVar.Conflict", // myVar is not in aliasMap
			wantResolved: "", // Should not match plain symbol if it's a selector on unknown object
			wantRaw:      "",
		},
		{
			name:         "nested in complex expression (no match)",
			funcName:     "Process",
			expr:         "db.Query().util.Process()", 
			wantResolved: "", // util here is a field, not a package alias at root
			wantRaw:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := parser.ParseExpr(tt.expr)
			if err != nil {
				// Some complex snippets might need full file parsing to be valid
				// but for most simple expressions ParseExpr is enough.
				t.Fatalf("ParseExpr(%q) failed: %v", tt.expr, err)
			}
			
			// We need a helper to find the target node within the parsed expression tree
			var foundResolved, foundRaw string
			var walk func(n ast.Node, insideSelector bool)
			walk = func(n ast.Node, insideSelector bool) {
				if n == nil || foundResolved != "" {
					return
				}
				switch node := n.(type) {
				case *ast.SelectorExpr:
					res, raw := matchSelectorExpr(node, aliasMap, tt.funcName)
					if res != "" {
						foundResolved, foundRaw = res, raw
						return
					}
					walk(node.X, false)
					walk(node.Sel, true)
					return
				case *ast.Ident:
					if !insideSelector {
						res, raw := matchIdent(node, aliasMap, tt.funcName)
						if res != "" {
							foundResolved, foundRaw = res, raw
						}
					}
					return
				}
				ast.Inspect(n, func(child ast.Node) bool {
					if child == nil || child == n {
						return true
					}
					walk(child, false)
					return false
				})
			}

			walk(parsed, false)

			if foundResolved != tt.wantResolved || foundRaw != tt.wantRaw {
				t.Errorf("%s: match failed\nexpr: %s\ngot:  (%q, %q)\nwant: (%q, %q)", 
					tt.name, tt.expr, foundResolved, foundRaw, tt.wantResolved, tt.wantRaw)
			}
		})
	}
}

func TestScanCallSites(t *testing.T) {
	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main

import (
	util "example.com/org/shared-lib/util"
	helper "example.com/org/shared-lib/helper"
	. "example.com/org/shared-lib/dot"
)

func main() {
	util.Must("prefix", "arg1")
	helper.DoWork(1, 2, 3)
	BareFunc("dot-import")
	_, _ = util.Calculate(42), helper.Format("hello")
}

func multiLine() {
	util.Must(
		"a",
		"b",
		"c",
	)
}

func nested() {
	util.Must("x", helper.Format("nested"))
	helper.DoWork(util.Calculate(7), 2, 3)
}

func unusedFunc() {}
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Second file with different alias — tests multi-file repos
	err = os.WriteFile(filepath.Join(dir, "other.go"), []byte(`package main

import u "example.com/org/shared-lib/util"

func fromOtherFile() {
	u.Must("a", "b")
}
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	target := "example.com/org/shared-lib"

	tests := []struct {
		label    string
		funcName string
		wantMin  int
	}{
		{"plain func (multi-file)", "Must", 3},    // main ×2 + other.go
		{"qualified func", "util.Must", 3},
		{"func in another pkg", "DoWork", 2},       // main + nested
		{"qualified other pkg", "helper.DoWork", 2},
		{"func returning value", "Calculate", 2},    // main + nested
		{"func with single arg", "Format", 2},       // main + nested
		{"dot import func", "BareFunc", 1},
		{"qualified dot import", "dot.BareFunc", 0},
		{"nonexistent func", "NoSuchFunc", 0},
		{"unused target func", "UnusedHelper", 0},
	}

	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			sites, warnings, err := ScanCallSites(dir, target, tt.funcName)
			if err != nil {
				t.Fatalf("ScanCallSites(%q): %v", tt.funcName, err)
			}
			for _, w := range warnings {
				t.Logf("warn: %s", w)
			}
			if len(sites) < tt.wantMin {
				t.Errorf("%q: got %d sites, want >= %d", tt.funcName, len(sites), tt.wantMin)
			}
			for _, s := range sites {
				t.Logf("  %s:%d %s (%d args)", s.File, s.Line, s.RawName, s.ArgCount)
			}
		})
	}
}
