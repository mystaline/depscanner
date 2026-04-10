package analysis

import (
	"go/ast"
	"go/parser"
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
