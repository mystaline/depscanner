package analysis

import (
	"fmt"
	"sort"
	"strings"
)

// ChangeKind classifies the type of change detected between two symbol snapshots.
type ChangeKind string

const (
	ChangeAdded        ChangeKind = "ADDED"
	ChangeRemoved      ChangeKind = "REMOVED"
	ChangeSignature    ChangeKind = "SIGNATURE_CHANGED"
	ChangeFieldAdded   ChangeKind = "FIELD_ADDED"
	ChangeFieldRemoved ChangeKind = "FIELD_REMOVED"
	ChangeFieldChanged ChangeKind = "FIELD_TYPE_CHANGED"
	ChangeMethodAdded  ChangeKind = "METHOD_ADDED"
	ChangeMethodRemoved ChangeKind = "METHOD_REMOVED"
	ChangeValueChanged ChangeKind = "VALUE_CHANGED"
	ChangeTypeChanged  ChangeKind = "TYPE_CHANGED"
)

// SymbolChange represents a single detected change between two snapshots.
type SymbolChange struct {
	Kind     ChangeKind `json:"kind"`
	Symbol   string     `json:"symbol"`
	Category SymbolKind `json:"category"`
	OldValue string     `json:"old_value,omitempty"`
	NewValue string     `json:"new_value,omitempty"`
	Breaking bool       `json:"breaking"`
}

// DiffSymbols compares two SymbolIndex snapshots and returns all changes.
func DiffSymbols(old, new SymbolIndex) []SymbolChange {
	var changes []SymbolChange

	// Removed symbols: in old but not in new.
	for key, oldSym := range old {
		if _, exists := new[key]; !exists {
			changes = append(changes, SymbolChange{
				Kind:     ChangeRemoved,
				Symbol:   key,
				Category: oldSym.Kind,
				OldValue: summarizeSymbol(oldSym),
				Breaking: true,
			})
		}
	}

	// Added symbols: in new but not in old.
	for key, newSym := range new {
		if _, exists := old[key]; !exists {
			changes = append(changes, SymbolChange{
				Kind:     ChangeAdded,
				Symbol:   key,
				Category: newSym.Kind,
				NewValue: summarizeSymbol(newSym),
				Breaking: false,
			})
		}
	}

	// Changed symbols: in both, compare detail.
	for key, oldSym := range old {
		newSym, exists := new[key]
		if !exists {
			continue
		}
		changes = append(changes, diffSymbolDetail(key, oldSym, newSym)...)
	}

	// Sort: breaking first, then by symbol name.
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Breaking != changes[j].Breaking {
			return changes[i].Breaking
		}
		return changes[i].Symbol < changes[j].Symbol
	})

	return changes
}

// diffSymbolDetail compares two versions of the same symbol.
func diffSymbolDetail(key string, old, new Symbol) []SymbolChange {
	switch old.Kind {
	case KindFunc, KindMethod:
		return diffFuncOrMethod(key, old, new)
	case KindStruct:
		return diffStruct(key, old, new)
	case KindInterface:
		return diffInterface(key, old, new)
	case KindConst, KindVar:
		return diffValue(key, old, new)
	case KindType:
		return diffType(key, old, new)
	}
	return nil
}

// diffFuncOrMethod compares function/method signatures.
func diffFuncOrMethod(key string, old, new Symbol) []SymbolChange {
	oldSig := formatSignature(old.Params, old.Returns, old.IsVariadic)
	newSig := formatSignature(new.Params, new.Returns, new.IsVariadic)

	if oldSig != newSig {
		return []SymbolChange{{
			Kind:     ChangeSignature,
			Symbol:   key,
			Category: old.Kind,
			OldValue: oldSig,
			NewValue: newSig,
			Breaking: true,
		}}
	}
	return nil
}

// diffStruct compares struct field lists.
func diffStruct(key string, old, new Symbol) []SymbolChange {
	var changes []SymbolChange

	oldFields := fieldMap(old.Fields)
	newFields := fieldMap(new.Fields)

	// Removed fields
	for name, of := range oldFields {
		if _, exists := newFields[name]; !exists {
			changes = append(changes, SymbolChange{
				Kind:     ChangeFieldRemoved,
				Symbol:   key,
				Category: KindStruct,
				OldValue: fmt.Sprintf("field %s %s", name, of.Type),
				Breaking: true,
			})
		}
	}

	// Added fields
	for name, nf := range newFields {
		if _, exists := oldFields[name]; !exists {
			changes = append(changes, SymbolChange{
				Kind:     ChangeFieldAdded,
				Symbol:   key,
				Category: KindStruct,
				NewValue: fmt.Sprintf("field %s %s", name, nf.Type),
				Breaking: false,
			})
		}
	}

	// Changed fields (type changed)
	for name, of := range oldFields {
		nf, exists := newFields[name]
		if !exists {
			continue
		}
		if of.Type != nf.Type {
			changes = append(changes, SymbolChange{
				Kind:     ChangeFieldChanged,
				Symbol:   key,
				Category: KindStruct,
				OldValue: fmt.Sprintf("field %s %s", name, of.Type),
				NewValue: fmt.Sprintf("field %s %s", name, nf.Type),
				Breaking: true,
			})
		}
	}

	return changes
}

// diffInterface compares interface method sets.
func diffInterface(key string, old, new Symbol) []SymbolChange {
	var changes []SymbolChange

	oldSet := toSet(old.Methods)
	newSet := toSet(new.Methods)

	for m := range oldSet {
		if _, exists := newSet[m]; !exists {
			changes = append(changes, SymbolChange{
				Kind:     ChangeMethodRemoved,
				Symbol:   key,
				Category: KindInterface,
				OldValue: m,
				Breaking: true,
			})
		}
	}

	for m := range newSet {
		if _, exists := oldSet[m]; !exists {
			changes = append(changes, SymbolChange{
				Kind:     ChangeMethodAdded,
				Symbol:   key,
				Category: KindInterface,
				NewValue: m,
				Breaking: true, // adding to interface breaks implementors
			})
		}
	}

	return changes
}

// diffValue compares const/var type and value.
func diffValue(key string, old, new Symbol) []SymbolChange {
	var changes []SymbolChange

	if old.TypeExpr != new.TypeExpr {
		changes = append(changes, SymbolChange{
			Kind:     ChangeTypeChanged,
			Symbol:   key,
			Category: old.Kind,
			OldValue: old.TypeExpr,
			NewValue: new.TypeExpr,
			Breaking: true,
		})
	}

	if old.Value != new.Value {
		changes = append(changes, SymbolChange{
			Kind:     ChangeValueChanged,
			Symbol:   key,
			Category: old.Kind,
			OldValue: old.Value,
			NewValue: new.Value,
			Breaking: false, // value change is usually non-breaking
		})
	}

	return changes
}

// diffType compares type alias/definition underlying types.
func diffType(key string, old, new Symbol) []SymbolChange {
	if old.TypeExpr != new.TypeExpr {
		return []SymbolChange{{
			Kind:     ChangeTypeChanged,
			Symbol:   key,
			Category: KindType,
			OldValue: old.TypeExpr,
			NewValue: new.TypeExpr,
			Breaking: true,
		}}
	}
	return nil
}

// formatSignature creates a string representation of a function signature.
func formatSignature(params, returns []ParamInfo, variadic bool) string {
	var parts []string
	for i, p := range params {
		typ := p.Type
		if variadic && i == len(params)-1 {
			typ = "..." + typ
		}
		if p.Name != "" {
			parts = append(parts, p.Name+" "+typ)
		} else {
			parts = append(parts, typ)
		}
	}

	sig := "(" + strings.Join(parts, ", ") + ")"

	if len(returns) > 0 {
		var retParts []string
		for _, r := range returns {
			if r.Name != "" {
				retParts = append(retParts, r.Name+" "+r.Type)
			} else {
				retParts = append(retParts, r.Type)
			}
		}
		if len(retParts) == 1 {
			sig += " " + retParts[0]
		} else {
			sig += " (" + strings.Join(retParts, ", ") + ")"
		}
	}

	return sig
}

// summarizeSymbol provides a single-line summary of a symbol for display.
func summarizeSymbol(s Symbol) string {
	switch s.Kind {
	case KindFunc, KindMethod:
		return formatSignature(s.Params, s.Returns, s.IsVariadic)
	case KindStruct:
		names := make([]string, len(s.Fields))
		for i, f := range s.Fields {
			names[i] = f.Name
		}
		return fmt.Sprintf("struct{%s}", strings.Join(names, ", "))
	case KindInterface:
		return fmt.Sprintf("interface{%s}", strings.Join(s.Methods, "; "))
	case KindConst, KindVar:
		if s.TypeExpr != "" {
			return s.TypeExpr + " = " + s.Value
		}
		return s.Value
	case KindType:
		return s.TypeExpr
	}
	return ""
}

// fieldMap converts a FieldInfo slice to a map keyed by field name.
func fieldMap(fields []FieldInfo) map[string]FieldInfo {
	m := make(map[string]FieldInfo, len(fields))
	for _, f := range fields {
		m[f.Name] = f
	}
	return m
}

// toSet converts a string slice to a set (map[string]struct{}).
func toSet(items []string) map[string]struct{} {
	m := make(map[string]struct{}, len(items))
	for _, item := range items {
		m[item] = struct{}{}
	}
	return m
}
