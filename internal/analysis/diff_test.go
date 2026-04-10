package analysis

import (
	"slices"
	"testing"
)

func TestDiffSymbolsRemoved(t *testing.T) {
	old := SymbolIndex{
		"pkg.FuncA": Symbol{
			Name:       "FuncA",
			Kind:       KindFunc,
			Package:    "pkg",
			IsExported: true,
			Params:     []ParamInfo{{Name: "x", Type: "int"}},
			Returns:    []ParamInfo{{Type: "error"}},
		},
	}
	new := SymbolIndex{}

	changes := DiffSymbols(old, new)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Kind != ChangeRemoved {
		t.Errorf("Kind = %v, want ChangeRemoved", changes[0].Kind)
	}
	if !changes[0].Breaking {
		t.Errorf("Breaking = false, want true")
	}
}

func TestDiffSymbolsAdded(t *testing.T) {
	old := SymbolIndex{}
	new := SymbolIndex{
		"pkg.FuncB": Symbol{
			Name:       "FuncB",
			Kind:       KindFunc,
			Package:    "pkg",
			IsExported: true,
			Params:     []ParamInfo{{Type: "string"}},
			Returns:    []ParamInfo{{Type: "int"}},
		},
	}

	changes := DiffSymbols(old, new)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Kind != ChangeAdded {
		t.Errorf("Kind = %v, want ChangeAdded", changes[0].Kind)
	}
	if changes[0].Breaking {
		t.Errorf("Breaking = true, want false (additive change)")
	}
}

func TestDiffSymbolsSignatureChanged(t *testing.T) {
	old := SymbolIndex{
		"pkg.Process": Symbol{
			Name:       "Process",
			Kind:       KindFunc,
			Package:    "pkg",
			IsExported: true,
			Params:     []ParamInfo{{Name: "data", Type: "string"}},
			Returns:    []ParamInfo{{Type: "error"}},
		},
	}
	new := SymbolIndex{
		"pkg.Process": Symbol{
			Name:       "Process",
			Kind:       KindFunc,
			Package:    "pkg",
			IsExported: true,
			Params:     []ParamInfo{{Name: "data", Type: "string"}, {Name: "opts", Type: "Options"}},
			Returns:    []ParamInfo{{Type: "error"}},
		},
	}

	changes := DiffSymbols(old, new)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Kind != ChangeSignature {
		t.Errorf("Kind = %v, want ChangeSignature", changes[0].Kind)
	}
	if !changes[0].Breaking {
		t.Errorf("Breaking = false, want true")
	}
}

func TestDiffSymbolsLogicChanged(t *testing.T) {
	old := SymbolIndex{
		"pkg.Calculate": Symbol{
			Name:       "Calculate",
			Kind:       KindFunc,
			Package:    "pkg",
			IsExported: true,
			Params:     []ParamInfo{{Type: "int"}},
			Returns:    []ParamInfo{{Type: "int"}},
			BodyHash:   "abc123",
		},
	}
	new := SymbolIndex{
		"pkg.Calculate": Symbol{
			Name:       "Calculate",
			Kind:       KindFunc,
			Package:    "pkg",
			IsExported: true,
			Params:     []ParamInfo{{Type: "int"}},
			Returns:    []ParamInfo{{Type: "int"}},
			BodyHash:   "def456", // Different body hash
		},
	}

	changes := DiffSymbols(old, new)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Kind != ChangeLogic {
		t.Errorf("Kind = %v, want ChangeLogic", changes[0].Kind)
	}
	if changes[0].Breaking {
		t.Errorf("Breaking = true, want false (logic change is non-breaking)")
	}
}

func TestDiffStructFieldRemoved(t *testing.T) {
	old := SymbolIndex{
		"pkg.User": Symbol{
			Name:       "User",
			Kind:       KindStruct,
			Package:    "pkg",
			IsExported: true,
			Fields: []FieldInfo{
				{Name: "ID", Type: "int"},
				{Name: "Name", Type: "string"},
			},
		},
	}
	new := SymbolIndex{
		"pkg.User": Symbol{
			Name:       "User",
			Kind:       KindStruct,
			Package:    "pkg",
			IsExported: true,
			Fields: []FieldInfo{
				{Name: "ID", Type: "int"},
			},
		},
	}

	changes := DiffSymbols(old, new)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Kind != ChangeFieldRemoved {
		t.Errorf("Kind = %v, want ChangeFieldRemoved", changes[0].Kind)
	}
	if !changes[0].Breaking {
		t.Errorf("Breaking = false, want true")
	}
}

func TestDiffStructFieldAdded(t *testing.T) {
	old := SymbolIndex{
		"pkg.Config": Symbol{
			Name:       "Config",
			Kind:       KindStruct,
			Package:    "pkg",
			IsExported: true,
			Fields: []FieldInfo{
				{Name: "Host", Type: "string"},
			},
		},
	}
	new := SymbolIndex{
		"pkg.Config": Symbol{
			Name:       "Config",
			Kind:       KindStruct,
			Package:    "pkg",
			IsExported: true,
			Fields: []FieldInfo{
				{Name: "Host", Type: "string"},
				{Name: "Port", Type: "int"},
			},
		},
	}

	changes := DiffSymbols(old, new)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Kind != ChangeFieldAdded {
		t.Errorf("Kind = %v, want ChangeFieldAdded", changes[0].Kind)
	}
	if changes[0].Breaking {
		t.Errorf("Breaking = true, want false (field addition is additive)")
	}
}

func TestDiffStructFieldTypeChanged(t *testing.T) {
	old := SymbolIndex{
		"pkg.Request": Symbol{
			Name:       "Request",
			Kind:       KindStruct,
			Package:    "pkg",
			IsExported: true,
			Fields: []FieldInfo{
				{Name: "Timeout", Type: "int"},
			},
		},
	}
	new := SymbolIndex{
		"pkg.Request": Symbol{
			Name:       "Request",
			Kind:       KindStruct,
			Package:    "pkg",
			IsExported: true,
			Fields: []FieldInfo{
				{Name: "Timeout", Type: "time.Duration"},
			},
		},
	}

	changes := DiffSymbols(old, new)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Kind != ChangeFieldChanged {
		t.Errorf("Kind = %v, want ChangeFieldChanged", changes[0].Kind)
	}
	if !changes[0].Breaking {
		t.Errorf("Breaking = false, want true")
	}
}

func TestDiffInterfaceMethodAdded(t *testing.T) {
	old := SymbolIndex{
		"pkg.Reader": Symbol{
			Name:       "Reader",
			Kind:       KindInterface,
			Package:    "pkg",
			IsExported: true,
			Methods:    []string{"Read(ctx context.Context, p []byte) (int, error)"},
		},
	}
	new := SymbolIndex{
		"pkg.Reader": Symbol{
			Name:       "Reader",
			Kind:       KindInterface,
			Package:    "pkg",
			IsExported: true,
			Methods: []string{
				"Read(ctx context.Context, p []byte) (int, error)",
				"ReadAll() ([]byte, error)",
			},
		},
	}

	changes := DiffSymbols(old, new)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Kind != ChangeMethodAdded {
		t.Errorf("Kind = %v, want ChangeMethodAdded", changes[0].Kind)
	}
	if !changes[0].Breaking {
		t.Errorf("Breaking = false, want true (interface method addition breaks implementations)")
	}
}

func TestDiffInterfaceMethodRemoved(t *testing.T) {
	old := SymbolIndex{
		"pkg.Writer": Symbol{
			Name:       "Writer",
			Kind:       KindInterface,
			Package:    "pkg",
			IsExported: true,
			Methods: []string{
				"Write(p []byte) (int, error)",
				"Close() error",
			},
		},
	}
	new := SymbolIndex{
		"pkg.Writer": Symbol{
			Name:       "Writer",
			Kind:       KindInterface,
			Package:    "pkg",
			IsExported: true,
			Methods:    []string{"Write(p []byte) (int, error)"},
		},
	}

	changes := DiffSymbols(old, new)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Kind != ChangeMethodRemoved {
		t.Errorf("Kind = %v, want ChangeMethodRemoved", changes[0].Kind)
	}
	if !changes[0].Breaking {
		t.Errorf("Breaking = false, want true")
	}
}

func TestDiffConstValueChanged(t *testing.T) {
	old := SymbolIndex{
		"pkg.MaxRetries": Symbol{
			Name:       "MaxRetries",
			Kind:       KindConst,
			Package:    "pkg",
			IsExported: true,
			Value:      "3",
			TypeExpr:   "int",
		},
	}
	new := SymbolIndex{
		"pkg.MaxRetries": Symbol{
			Name:       "MaxRetries",
			Kind:       KindConst,
			Package:    "pkg",
			IsExported: true,
			Value:      "5",
			TypeExpr:   "int",
		},
	}

	changes := DiffSymbols(old, new)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Kind != ChangeValueChanged {
		t.Errorf("Kind = %v, want ChangeValueChanged", changes[0].Kind)
	}
	if changes[0].Breaking {
		t.Errorf("Breaking = true, want false (value change is non-breaking)")
	}
}

func TestDiffTypeExprChanged(t *testing.T) {
	old := SymbolIndex{
		"pkg.Result": Symbol{
			Name:       "Result",
			Kind:       KindType,
			Package:    "pkg",
			IsExported: true,
			TypeExpr:   "string",
		},
	}
	new := SymbolIndex{
		"pkg.Result": Symbol{
			Name:       "Result",
			Kind:       KindType,
			Package:    "pkg",
			IsExported: true,
			TypeExpr:   "[]string",
		},
	}

	changes := DiffSymbols(old, new)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Kind != ChangeTypeChanged {
		t.Errorf("Kind = %v, want ChangeTypeChanged", changes[0].Kind)
	}
	if !changes[0].Breaking {
		t.Errorf("Breaking = false, want true")
	}
}

func TestDiffSortingByBreakingFirst(t *testing.T) {
	old := SymbolIndex{
		"pkg.A": Symbol{Name: "A", Kind: KindFunc, Package: "pkg", IsExported: true, BodyHash: "old1"},
		"pkg.B": Symbol{Name: "B", Kind: KindFunc, Package: "pkg", IsExported: true},
	}
	new := SymbolIndex{
		"pkg.A": Symbol{Name: "A", Kind: KindFunc, Package: "pkg", IsExported: true, BodyHash: "new1"}, // logic change
		"pkg.B": Symbol{Name: "B", Kind: KindFunc, Package: "pkg", IsExported: true, Params: []ParamInfo{{Type: "int"}}}, // signature changed
	}

	changes := DiffSymbols(old, new)

	if len(changes) < 2 {
		t.Fatalf("expected at least 2 changes, got %d", len(changes))
	}
	// Breaking changes should come first
	if !changes[0].Breaking && changes[1].Breaking {
		t.Error("changes not sorted with breaking first")
	}
}

func TestDiffNoChanges(t *testing.T) {
	index := SymbolIndex{
		"pkg.Unchanged": Symbol{
			Name:       "Unchanged",
			Kind:       KindFunc,
			Package:    "pkg",
			IsExported: true,
			BodyHash:   "same",
		},
	}

	changes := DiffSymbols(index, index)

	if len(changes) != 0 {
		t.Errorf("expected 0 changes for identical indices, got %d", len(changes))
	}
}

func TestDiffInternalSymbolNotReported(t *testing.T) {
	old := SymbolIndex{
		"pkg.internalFunc": Symbol{
			Name:       "internalFunc",
			Kind:       KindFunc,
			Package:    "pkg",
			IsExported: false, // internal
		},
	}
	new := SymbolIndex{}

	changes := DiffSymbols(old, new)

	// Internal symbols should not be reported unless they are root cause of a change
	// In this case, it's removed but not exported, so should not appear
	if len(changes) != 0 {
		t.Errorf("expected internal symbol removal not to be reported, got %d changes", len(changes))
	}
}

func TestDiffPropagationAffected(t *testing.T) {
	// Create a scenario where A uses B, and B changes
	old := SymbolIndex{
		"pkg.B": Symbol{
			Name:       "B",
			Kind:       KindFunc,
			Package:    "pkg",
			IsExported: true,
			BodyHash:   "oldB",
		},
		"pkg.A": Symbol{
			Name:        "A",
			Kind:        KindFunc,
			Package:     "pkg",
			IsExported:  true,
			UsedSymbols: []string{"B"}, // A uses B
		},
	}
	new := SymbolIndex{
		"pkg.B": Symbol{
			Name:       "B",
			Kind:       KindFunc,
			Package:    "pkg",
			IsExported: true,
			BodyHash:   "newB", // B logic changed
		},
		"pkg.A": Symbol{
			Name:        "A",
			Kind:        KindFunc,
			Package:     "pkg",
			IsExported:  true,
			UsedSymbols: []string{"B"},
		},
	}

	changes := DiffSymbols(old, new)

	// Should report B as LOGIC_CHANGED and A as AFFECTED
	hasLogicChange := slices.ContainsFunc(changes, func(c SymbolChange) bool {
		return c.Kind == ChangeLogic && c.Symbol == "pkg.B"
	})
	hasAffected := slices.ContainsFunc(changes, func(c SymbolChange) bool {
		return c.Kind == ChangeAffected && c.Symbol == "pkg.A"
	})

	if !hasLogicChange {
		t.Error("expected LOGIC_CHANGED for B")
	}
	if !hasAffected {
		t.Error("expected AFFECTED for A")
	}
}
