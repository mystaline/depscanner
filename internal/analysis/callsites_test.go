package analysis

import (
	"go/ast"
	"go/parser"
	"testing"
)

func TestMatchCallExpr(t *testing.T) {
	aliasMap := map[string]string{
		"util": "github.com/org/lib/util",
		"db":   "github.com/org/lib/db",
		".":    "github.com/org/lib/dot",
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
			name:         "method call on object (heuristic)",
			funcName:     "Cancel",
			expr:         "agenda.Cancel()",
			wantResolved: "agenda.Cancel",
			wantRaw:      "agenda.Cancel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := parser.ParseExpr(tt.expr)
			if err != nil {
				t.Fatalf("ParseExpr(%q) failed: %v", tt.expr, err)
			}
			call := parsed.(*ast.CallExpr)

			gotResolved, gotRaw := matchCallExpr(call, aliasMap, tt.funcName)
			if gotResolved != tt.wantResolved || gotRaw != tt.wantRaw {
				t.Errorf("matchCallExpr() = (%q, %q), want (%q, %q)", 
					gotResolved, gotRaw, tt.wantResolved, tt.wantRaw)
			}
		})
	}
}
