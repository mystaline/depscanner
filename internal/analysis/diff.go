package analysis

import (
	"fmt"
	"sort"
	"strings"
)

// ChangeKind classifies the type of change detected between two symbol snapshots.
type ChangeKind string

const (
	ChangeAdded         ChangeKind = "ADDED"
	ChangeRemoved       ChangeKind = "REMOVED"
	ChangeSignature     ChangeKind = "SIGNATURE_CHANGED"
	ChangeFieldAdded    ChangeKind = "FIELD_ADDED"
	ChangeFieldRemoved  ChangeKind = "FIELD_REMOVED"
	ChangeFieldChanged  ChangeKind = "FIELD_TYPE_CHANGED"
	ChangeMethodAdded   ChangeKind = "METHOD_ADDED"
	ChangeMethodRemoved ChangeKind = "METHOD_REMOVED"
	ChangeValueChanged  ChangeKind = "VALUE_CHANGED"
	ChangeTypeChanged   ChangeKind = "TYPE_CHANGED"

	// Behavioral Audit (Hybrid Approach)
	ChangeLogic    ChangeKind = "LOGIC_CHANGED" // The implementation (BodyHash) changed
	ChangeAffected ChangeKind = "AFFECTED"      // One of its dependencies changed
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
// It performs behavioral impact analysis by propagating logic changes
// through the internal call graph (Hybrid Approach).
func DiffSymbols(old, new SymbolIndex) []SymbolChange {
	rawChanges := make(map[string]SymbolChange)

	// 1. Identify Direct Changes
	for key, oldSym := range old {
		newSym, exists := new[key]
		if !exists {
			rawChanges[key] = SymbolChange{
				Kind:     ChangeRemoved,
				Symbol:   key,
				Category: oldSym.Kind,
				OldValue: summarizeSymbol(oldSym),
				Breaking: true,
			}
			continue
		}

		// Signature/Structural changes take precedence over logic changes
		details := diffSymbolDetail(key, oldSym, newSym)
		if len(details) > 0 {
			// If signature changed, it's breaking.
			// We only pick the first/most important detail for the summary.
			rawChanges[key] = details[0]
		} else if oldSym.BodyHash != "" && newSym.BodyHash != "" && oldSym.BodyHash != newSym.BodyHash {
			// Body changed but signature is same.
			rawChanges[key] = SymbolChange{
				Kind:     ChangeLogic,
				Symbol:   key,
				Category: oldSym.Kind,
				Breaking: false,
			}
		}
	}

	// Added symbols
	for key, newSym := range new {
		if _, exists := old[key]; !exists {
			rawChanges[key] = SymbolChange{
				Kind:     ChangeAdded,
				Symbol:   key,
				Category: newSym.Kind,
				NewValue: summarizeSymbol(newSym),
				Breaking: false,
			}
		}
	}

	// 2. Propagate Impact (Fix-point iteration)
	// If symbol A uses symbol B, and B changed, then A is AFFECTED.
	affected := make(map[string]struct{})
	for {
		changedInLoop := false
		for key, sym := range new {
			// If already marked as changed directly, skip
			if _, exists := rawChanges[key]; exists {
				continue
			}
			// If already marked as affected, skip
			if _, exists := affected[key]; exists {
				continue
			}

			// Check if any of the symbols it uses have changed or are affected
			for _, depName := range sym.UsedSymbols {
				// The depName in UsedSymbols is a simple name (heuristic).
				// We check if any symbol in the same package with that name changed.
				fullDepKey := symbolKey(sym.Package, "", depName)
				if _, isDirect := rawChanges[fullDepKey]; isDirect {
					affected[key] = struct{}{}
					changedInLoop = true
					break
				}
				if _, isAffected := affected[fullDepKey]; isAffected {
					affected[key] = struct{}{}
					changedInLoop = true
					break
				}
			}
		}
		if !changedInLoop {
			break
		}
	}

	// 3. Finalize result: only report Exported symbols or Direct Changes.
	var finalChanges []SymbolChange
	for key, change := range rawChanges {
		sym, exists := new[key]
		// Always report direct changes to exported symbols.
		// For internal symbols, we only report them if they are the ROOT cause of a change.
		if (exists && sym.IsExported) || (!exists && old[key].IsExported) {
			finalChanges = append(finalChanges, change)
		}
	}

	for key := range affected {
		sym := new[key]
		if sym.IsExported {
			finalChanges = append(finalChanges, SymbolChange{
				Kind:     ChangeAffected,
				Symbol:   key,
				Category: sym.Kind,
				Breaking: false,
			})
		}
	}

	// Sort: breaking first, then by symbol name.
	sort.Slice(finalChanges, func(i, j int) bool {
		if finalChanges[i].Breaking != finalChanges[j].Breaking {
			return finalChanges[i].Breaking
		}
		return finalChanges[i].Symbol < finalChanges[j].Symbol
	})

	return finalChanges
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

func diffStruct(key string, old, new Symbol) []SymbolChange {
	var changes []SymbolChange
	oldFields := fieldMap(old.Fields)
	newFields := fieldMap(new.Fields)
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
				Breaking: true,
			})
		}
	}
	return changes
}

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
			Breaking: false,
		})
	}
	return changes
}

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

func fieldMap(fields []FieldInfo) map[string]FieldInfo {
	m := make(map[string]FieldInfo, len(fields))
	for _, f := range fields {
		m[f.Name] = f
	}
	return m
}

func toSet(items []string) map[string]struct{} {
	m := make(map[string]struct{}, len(items))
	for _, item := range items {
		m[item] = struct{}{}
	}
	return m
}

func symbolKey(pkg, receiver, name string) string {
	if receiver != "" {
		return fmt.Sprintf("%s.%s.%s", pkg, receiver, name)
	}
	return fmt.Sprintf("%s.%s", pkg, name)
}
