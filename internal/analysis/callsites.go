package analysis

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// CallSite represents a single location where a target function is called.
type CallSite struct {
	File     string // relative path within the repo
	Line     int
	Column   int
	FuncName string // the resolved function name (e.g. "github.com/org/lib/helper.Must")
	RawName  string // the original name in code (e.g. "helper.Must")
	ArgCount int    // number of arguments in the call

	// Set when the call was detected via a DI-injected struct field.
	// e.g. u.SchedulerService.Schedule(...) sets ViaField="SchedulerService"
	ViaField     string
	ViaFieldType string // fully qualified, e.g. "github.com/org/lib/service.SchedulerService"

	// Set when the call was detected via a local variable assigned from a
	// target-module constructor/method call, or via a fluent call chain
	// rooted at one. e.g. `pipe := pipeline.NewPipelineBuilder(); pipe.Group(...)`
	// sets ViaLocalVar="pipe"; a chain like
	// `pipeline.NewPipelineBuilder().Group(...)` has no variable, so
	// ViaLocalVar="" but ViaLocalVarType is still populated.
	ViaLocalVar     string
	ViaLocalVarType string // fully qualified, e.g. "github.com/org/lib/pipeline.PipelineBuilder"
}

// diFieldEntry records that a named struct field has a type originating from the target module.
type diFieldEntry struct {
	importPath string // e.g. "github.com/org/lib/service"
	typeName   string // e.g. "SchedulerService"
}

// ScanSymbolReferences walks a repo directory and finds all references to symbolName
// within packages imported from targetModule. Handles both call expressions
// (func/method calls) and plain identifier references (const, var).
//
// symbolName can be:
//   - A plain name like "ErrNotFound" — matches in any sub-package of targetModule
//   - A qualified name like "helper.Must" — matches only in that sub-package
//
// Two-pass optimization: first pass uses ImportsOnly to filter files that
// import the target module, second pass does full AST parse only on those files.
//
// The returned warnings list contains diagnostics for files that passed the
// import-only parse but failed full AST parse (likely syntax errors).
func ScanSymbolReferences(repoDir, targetModule, symbolName string, registry ReturnTypeRegistry) ([]CallSite, []string, error) {
	qualPkg, plainFunc := splitQualifiedName(symbolName)
	if plainFunc == "" {
		return nil, nil, fmt.Errorf("invalid symbol name %q: missing name component", symbolName)
	}

	// Pass 1: single walk collects two sets of candidates.
	//
	//  • candidates / candidateDirs  — files whose imports match qualPkg (for direct calls)
	//  • diCandidates / diCandidateDirs — files importing ANY part of target module
	//                                     (for DI field index; ignores qualPkg because
	//                                     qualPkg is a pkg-level filter but a type qualifier
	//                                     like "SchedulerService" won't match the "service" pkg)
	type fileImport struct {
		path     string
		aliasMap map[string]string // alias/pkg name -> full import path
	}
	var candidates []fileImport
	candidateDirs := make(map[string]bool)
	var diCandidates []fileImport
	diCandidateDirs := make(map[string]bool)
	fset := token.NewFileSet()

	err := WalkGoFiles(repoDir, func(path string) error {
		f, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return nil // skip unparseable
		}

		// allAliases: every import from target module (used for DI field scanning)
		allAliases := make(map[string]string)
		// filteredAliases: only imports matching qualPkg (used for direct call scanning)
		filteredAliases := make(map[string]string)

		for _, imp := range f.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if importPath != targetModule && !strings.HasPrefix(importPath, targetModule+"/") {
				continue
			}

			parts := strings.Split(importPath, "/")
			pkgBaseName := parts[len(parts)-1]
			isDot := imp.Name != nil && imp.Name.Name == "."

			var localName string
			if imp.Name != nil {
				localName = imp.Name.Name
			} else {
				localName = pkgBaseName
			}

			// Always add to allAliases (DI field index needs every target-module import).
			allAliases[localName] = importPath

			// Add to filteredAliases only when the import matches the qualPkg filter.
			matchesQual := true
			if qualPkg != "" && !isDot {
				relPath := ""
				if importPath != targetModule {
					relPath = strings.TrimPrefix(importPath, targetModule+"/")
				}
				// Method support: qualPkg might be "pkg.Receiver", so we check prefix
				if relPath != qualPkg && pkgBaseName != qualPkg && !strings.HasPrefix(qualPkg, relPath+".") {
					matchesQual = false
				}
			}
			if matchesQual {
				filteredAliases[localName] = importPath
			}
		}

		if len(allAliases) > 0 {
			diCandidates = append(diCandidates, fileImport{path: path, aliasMap: allAliases})
			diCandidateDirs[filepath.Dir(path)] = true
		}
		if len(filteredAliases) > 0 {
			candidates = append(candidates, fileImport{path: path, aliasMap: filteredAliases})
			candidateDirs[filepath.Dir(path)] = true
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("walk repo: %w", err)
	}

	// No files import the target module at all — nothing to do.
	if len(diCandidates) == 0 {
		return nil, nil, nil
	}

	// Pass 1b: build DI index from all target-module importers.
	// Covers both struct fields (obj.field.method) and function parameters (param.method).
	// Uses allAliases so type qualifiers like "SchedulerService.Schedule" find names whose
	// type lives in package "service" regardless of the qualPkg filter.
	diFieldIndex := make(map[string][]diFieldEntry) // name → entries (fields + params)
	fsetDI := token.NewFileSet()
	for _, c := range diCandidates {
		f, parseErr := parser.ParseFile(fsetDI, c.path, nil, 0)
		if parseErr != nil {
			continue
		}
		extractDIFields(f, c.aliasMap, diFieldIndex)
		extractFuncParams(f, c.aliasMap, diFieldIndex)
	}

	// Pass 2: full AST parse on candidate files (qualPkg-filtered), find direct CallExpr.
	// Use a fresh FileSet to avoid unbounded offset growth from double-parsing.
	fset2 := token.NewFileSet()
	var sites []CallSite
	var parseWarnings []string
	scannedInPass2 := make(map[string]bool)

	for _, c := range candidates {
		scannedInPass2[c.path] = true
		f, parseErr := parser.ParseFile(fset2, c.path, nil, parser.ParseComments)
		if parseErr != nil {
			parseWarnings = append(parseWarnings, fmt.Sprintf("%s: %v", c.path, parseErr))
			continue
		}

		var walk func(n ast.Node, insideSelector bool)
		walk = func(n ast.Node, insideSelector bool) {
			if n == nil {
				return
			}

			switch node := n.(type) {
			case *ast.CallExpr:
				// First check if the Fun of this CallExpr matches
				var resolvedName, rawName string
				switch fun := node.Fun.(type) {
				case *ast.SelectorExpr:
					resolvedName, rawName = matchSelectorExpr(fun, c.aliasMap, plainFunc)
				case *ast.Ident:
					if !insideSelector {
						resolvedName, rawName = matchIdent(fun, c.aliasMap, plainFunc)
					}
				}

				if resolvedName != "" {
					// We found a CALL site!
					addSite(fset2, &sites, node, repoDir, resolvedName, rawName)
					// We still want to walk the arguments of the call (they might contain other calls)
					for _, arg := range node.Args {
						walk(arg, false)
					}
					// IMPORTANT: Do NOT walk node.Fun because we already processed it
					return
				}

			case *ast.SelectorExpr:
				resolvedName, rawName := matchSelectorExpr(node, c.aliasMap, plainFunc)
				if resolvedName != "" {
					// We found a REFERENCE (not necessarily a call)
					addSite(fset2, &sites, node, repoDir, resolvedName, rawName)
					return // matched, stop descending
				}
				// Descend into X (might be a package or another object)
				walk(node.X, false)
				// Sel is inside a selector, mark it so dot-import logic skips it
				walk(node.Sel, true)
				return

			case *ast.Ident:
				if !insideSelector {
					resolvedName, rawName := matchIdent(node, c.aliasMap, plainFunc)
					if resolvedName != "" {
						addSite(fset2, &sites, node, repoDir, resolvedName, rawName)
					}
				}
				return
			}

			// Generic descent for all other nodes
			ast.Inspect(n, func(child ast.Node) bool {
				if child == nil || child == n {
					return true
				}
				walk(child, false)
				return false
			})
		}

		walk(f, false)

		// Also scan this candidate for DI calls (obj.field.method) and param calls (param.method).
		if len(diFieldIndex) > 0 {
			sites = append(sites, scanDICallSites(f, fset2, repoDir, diFieldIndex, plainFunc, qualPkg)...)
			sites = append(sites, scanParamCallSites(f, fset2, repoDir, diFieldIndex, plainFunc, qualPkg)...)
		}

		// Local-variable and chained-call detection: does not require the
		// DI field index, only the alias map for this file and the
		// source module's return-type registry.
		// c.aliasMap here is filteredAliases (qualPkg-scoped), not allAliases — fine today since
		// the registry only tracks same-package return types; revisit if it grows cross-package tracking.
		sites = append(sites, scanReturnTypeCallSites(f, fset2, repoDir, c.aliasMap, registry, plainFunc, qualPkg)...)
	}

	// Pass 3: scan sibling files (same dirs as DI candidates, not yet scanned) for DI calls.
	// These files don't import the target module directly but may call methods on DI fields.
	// Uses diCandidateDirs (not candidateDirs) so type-qualifier searches like
	// "SchedulerService.Schedule" still find callers even when no direct-call candidates exist.
	if len(diFieldIndex) > 0 || len(registry.Funcs) > 0 || len(registry.Methods) > 0 {
		fset3 := token.NewFileSet()
		if walkErr := WalkGoFiles(repoDir, func(path string) error {
			if scannedInPass2[path] {
				return nil
			}
			if !diCandidateDirs[filepath.Dir(path)] {
				return nil
			}
			f, parseErr := parser.ParseFile(fset3, path, nil, parser.ParseComments)
			if parseErr != nil {
				parseWarnings = append(parseWarnings, fmt.Sprintf("%s: %v", path, parseErr))
				return nil
			}
			if len(diFieldIndex) > 0 {
				sites = append(sites, scanDICallSites(f, fset3, repoDir, diFieldIndex, plainFunc, qualPkg)...)
				sites = append(sites, scanParamCallSites(f, fset3, repoDir, diFieldIndex, plainFunc, qualPkg)...)
			}
			// Need this file's own alias map for the registry-based scan —
			// diCandidates was built with allAliases per file in Pass 1; find it.
			for _, c := range diCandidates {
				if c.path == path {
					sites = append(sites, scanReturnTypeCallSites(f, fset3, repoDir, c.aliasMap, registry, plainFunc, qualPkg)...)
					break
				}
			}
			return nil
		}); walkErr != nil {
			parseWarnings = append(parseWarnings, fmt.Sprintf("pass3 walk: %v", walkErr))
		}
	}

	// Deduplicate sites by (File, Line, Column) to guard against any double-scan edge cases
	// where both the direct-call heuristic and the DI scanner fire for the same expression.
	// DI sites (ViaField != "") are preferred over plain sites at the same position.
	sites = deduplicateSites(sites)

	return sites, parseWarnings, nil
}

// deduplicateSites removes duplicate CallSites at the same (File, Line, Column).
// When a tagged site (DI or local-var/chain) and an untagged site share a
// position, the tagged site is kept.
func deduplicateSites(sites []CallSite) []CallSite {
	type key struct{ file string; line, col int }
	seen := make(map[key]int, len(sites)) // key → index in result
	result := make([]CallSite, 0, len(sites))
	tagged := func(s CallSite) bool { return s.ViaField != "" || s.ViaLocalVar != "" || s.ViaLocalVarType != "" }
	for _, s := range sites {
		k := key{s.File, s.Line, s.Column}
		if idx, dup := seen[k]; dup {
			if tagged(s) && !tagged(result[idx]) {
				result[idx] = s
			}
			continue
		}
		seen[k] = len(result)
		result = append(result, s)
	}
	return result
}

// extractDIFields scans a parsed file for struct fields whose type comes from the target module
// (as indicated by aliasMap), and populates the shared diFieldIndex.
//
// Supported field type forms:
//   - pkg.Type, *pkg.Type            — direct or pointer
//   - []pkg.Type, []*pkg.Type        — slice of direct or pointer
//   - Type (dot-import)              — bare ident via `import . "pkg"`
//
// Known limitations: embedded (anonymous) fields, map/chan field types, and
// multi-level slice types ([][]*T) are not indexed.
func extractDIFields(f *ast.File, aliasMap map[string]string, index map[string][]diFieldEntry) {
	ast.Inspect(f, func(n ast.Node) bool {
		st, ok := n.(*ast.StructType)
		if !ok {
			return true
		}
		for _, field := range st.Fields.List {
			if field.Type == nil || len(field.Names) == 0 {
				// len(Names)==0 means anonymous/embedded field — not tracked (requires
				// type info to resolve promoted method calls like h.Schedule()).
				continue
			}
			addDIFieldEntry(field, aliasMap, index)
		}
		return true
	})
}

// addDIFieldEntry resolves a single struct field's type and adds it to index if it
// comes from the target module. Handles pointer, slice-of-pointer, and dot-import forms.
func addDIFieldEntry(field *ast.Field, aliasMap map[string]string, index map[string][]diFieldEntry) {
	expr := field.Type

	// Unwrap pointer: *T → T
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	// Unwrap slice: []T or []*T → T
	if arr, ok := expr.(*ast.ArrayType); ok {
		expr = arr.Elt
		if star, ok2 := expr.(*ast.StarExpr); ok2 {
			expr = star.X
		}
	}

	var entry diFieldEntry
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		// pkg.Type (normal import)
		pkgIdent, ok := e.X.(*ast.Ident)
		if !ok {
			return
		}
		importPath, isTarget := aliasMap[pkgIdent.Name]
		if !isTarget {
			return
		}
		entry = diFieldEntry{importPath: importPath, typeName: e.Sel.Name}

	case *ast.Ident:
		// Bare type name from a dot-import (import . "pkg")
		importPath, isDot := aliasMap["."]
		if !isDot {
			return
		}
		entry = diFieldEntry{importPath: importPath, typeName: e.Name}

	default:
		return
	}

	for _, nameIdent := range field.Names {
		existing := index[nameIdent.Name]
		dup := false
		for _, e := range existing {
			if e.importPath == entry.importPath && e.typeName == entry.typeName {
				dup = true
				break
			}
		}
		if !dup {
			index[nameIdent.Name] = append(existing, entry)
		}
	}
}

// extractFuncParams scans function declarations for parameters typed from the target module
// and adds them to the shared index. This enables detection of param.method() calls
// (single-level selectors) in addition to the struct-field obj.field.method() pattern.
func extractFuncParams(f *ast.File, aliasMap map[string]string, index map[string][]diFieldEntry) {
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}
		if fn.Type.Params == nil {
			return true
		}
		for _, field := range fn.Type.Params.List {
			if field.Type == nil || len(field.Names) == 0 {
				continue
			}
			addDIFieldEntry(field, aliasMap, index)
		}
		return true
	})
}

// scanParamCallSites walks a file looking for single-level selector calls param.method(...)
// where param is a known name (struct field or function parameter) from the target module.
// Complements scanDICallSites which handles the two-level obj.field.method() pattern.
func scanParamCallSites(f *ast.File, fset *token.FileSet, repoDir string, index map[string][]diFieldEntry, methodName, typeQualifier string) []CallSite {
	var sites []CallSite
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name != methodName {
			return true
		}
		// Must be a plain Ident (single-level). Chained selectors are handled by scanDICallSites.
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		entries, exists := index[ident.Name]
		if !exists {
			return true
		}
		for _, entry := range entries {
			if typeQualifier != "" && !matchesDIQualifier(entry, typeQualifier) {
				continue
			}
			pos := fset.Position(call.Pos())
			relPath, relErr := filepath.Rel(repoDir, pos.Filename)
			if relErr != nil {
				relPath = pos.Filename
			}
			sites = append(sites, CallSite{
				File:         filepath.ToSlash(relPath),
				Line:         pos.Line,
				Column:       pos.Column,
				FuncName:     entry.importPath + "." + methodName,
				RawName:      ident.Name + "." + methodName,
				ArgCount:     len(call.Args),
				ViaField:     ident.Name,
				ViaFieldType: entry.importPath + "." + entry.typeName,
			})
			break
		}
		return true
	})
	return sites
}

// scanDICallSites walks a file looking for chained selector calls of the form obj.field.method(...)
// where field is a known DI field from the target module and method matches the searched symbol.
//
// typeQualifier (the qualPkg portion of the symbol) is matched via matchesDIQualifier, which
// handles bare type names ("SchedulerService"), package segments ("service"), and the composite
// "pkgseg.TypeName" form produced by the impact command ("service.SchedulerService").
//
// All matching entries for a given field name are emitted (one CallSite per entry), so when
// two structs in the same package declare a field with the same name but different target-module
// types, both are reported with distinct ViaFieldType values.
func scanDICallSites(f *ast.File, fset *token.FileSet, repoDir string, diFieldIndex map[string][]diFieldEntry, methodName, typeQualifier string) []CallSite {
	var sites []CallSite
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		outerSel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		// outerSel.Sel is the method being called.
		if outerSel.Sel.Name != methodName {
			return true
		}
		// outerSel.X must be another SelectorExpr representing the field access.
		innerSel, ok := outerSel.X.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		fieldName := innerSel.Sel.Name
		entries, exists := diFieldIndex[fieldName]
		if !exists {
			return true
		}
		for _, entry := range entries {
			// typeQualifier can be:
			//   ""                      — match everything
			//   "SchedulerService"         — type name only
			//   "service"               — package segment only
			//   "service.SchedulerService" — pkg.Type composite (built by impact command)
			if typeQualifier != "" && !matchesDIQualifier(entry, typeQualifier) {
				continue
			}
			pos := fset.Position(call.Pos())
			relPath, relErr := filepath.Rel(repoDir, pos.Filename)
			if relErr != nil {
				relPath = pos.Filename
			}
			// FuncName uses pkg.Method (not pkg.Type.Method) so matchesCallSite in
			// AnalyzeImpact can do an exact package match. Type info is in ViaFieldType.
			sites = append(sites, CallSite{
				File:         filepath.ToSlash(relPath),
				Line:         pos.Line,
				Column:       pos.Column,
				FuncName:     entry.importPath + "." + methodName,
				RawName:      fieldName + "." + methodName,
				ArgCount:     len(call.Args),
				ViaField:     fieldName,
				ViaFieldType: entry.importPath + "." + entry.typeName,
			})
			// Continue — emit one site per qualifying entry (multiple entries can exist
			// when different structs in the same package share a field name with different types).
		}
		return true
	})
	return sites
}

// matchesDIQualifier reports whether a diFieldEntry satisfies the given typeQualifier.
// Handles three qualifier forms:
//   - "SchedulerService"         → match by type name
//   - "service"               → match by last path segment of import path
//   - "service.SchedulerService" → match by both (composite form from impact command)
func matchesDIQualifier(entry diFieldEntry, typeQualifier string) bool {
	return matchesQualifier(entry.importPath, entry.typeName, typeQualifier)
}

// matchesQualifier reports whether (importPath, typeName) satisfies typeQualifier.
// Handles three qualifier forms:
//   - "SchedulerService"         → match by type name
//   - "service"               → match by last path segment of import path
//   - "service.SchedulerService" → match by both (composite form)
func matchesQualifier(importPath, typeName, typeQualifier string) bool {
	pkgSuffix := importPath
	if idx := strings.LastIndex(pkgSuffix, "/"); idx >= 0 {
		pkgSuffix = pkgSuffix[idx+1:]
	}
	if typeName == typeQualifier || pkgSuffix == typeQualifier {
		return true
	}
	if dotIdx := strings.LastIndex(typeQualifier, "."); dotIdx >= 0 {
		qualPkgSeg := typeQualifier[:dotIdx]
		qualType := typeQualifier[dotIdx+1:]
		if typeName != qualType {
			return false
		}
		// qualPkgSeg can be "helper" (single) or "module/subpkg/util" (sub-pkg).
		// Match suffix rather than single pkgSuffix for sub-package support.
		return strings.HasSuffix(importPath, "/"+qualPkgSeg) || importPath == qualPkgSeg || pkgSuffix == qualPkgSeg
	}
	return false
}

func addSite(fset *token.FileSet, sites *[]CallSite, n ast.Node, repoDir, resolved, raw string) {
	pos := fset.Position(n.Pos())
	relPath, relErr := filepath.Rel(repoDir, pos.Filename)
	if relErr != nil {
		relPath = pos.Filename
	}

	// Determine arg count if it's a call
	argCount := 0
	if call, ok := n.(*ast.CallExpr); ok {
		argCount = len(call.Args)
	}

	*sites = append(*sites, CallSite{
		File:     filepath.ToSlash(relPath),
		Line:     pos.Line,
		Column:   pos.Column,
		FuncName: resolved,
		RawName:  raw,
		ArgCount: argCount,
	})
}

// matchSelectorExpr checks if a SelectorExpr matches the target symbol.
func matchSelectorExpr(sel *ast.SelectorExpr, aliasMap map[string]string, symbolName string) (string, string) {
	siteFuncName := sel.Sel.Name

	// Handle complex symbols like "Receiver.Method"
	targetReceiver, targetName := splitSymbolKey(symbolName)

	if targetName != "" {
		// It's a method/field with a receiver
		if siteFuncName != targetName {
			return "", ""
		}
	} else {
		// Plain function or symbol
		if siteFuncName != symbolName {
			return "", ""
		}
	}

	// The X part must be an Ident (the package alias or object name)
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", ""
	}
	pkgAlias := ident.Name

	importPath, isTarget := aliasMap[pkgAlias]
	if isTarget {
		return importPath + "." + siteFuncName, pkgAlias + "." + siteFuncName
	}

	// Heuristic for method calls on objects
	if targetReceiver != "" && pkgAlias != "" {
		// If we find "obj.Method" and we are looking for "Receiver.Method",
		// we match by name since we don't have type info.
		return pkgAlias + "." + siteFuncName, pkgAlias + "." + siteFuncName
	}

	return "", ""
}

// matchIdent checks if an Ident matches the target symbol (dot-imports).
func matchIdent(id *ast.Ident, aliasMap map[string]string, symbolName string) (string, string) {
	if id.Name != symbolName {
		return "", ""
	}

	importPath, hasDot := aliasMap["."]
	if !hasDot {
		return "", ""
	}
	return importPath + "." + id.Name, id.Name
}

// resolveExprReturnType attempts to determine which target-module type a Go
// expression evaluates to, using the return-type registry and a
// function-scoped local-variable index. Handles three forms:
//   - pkg.NewX(...)      — direct constructor call, resolved via aliasMap + registry.Funcs
//   - knownVar           — a local variable already present in localTypes
//   - <expr>.Method(...) — recurses on <expr>, then looks up Method in
//     registry.Methods for <expr>'s resolved type
//
// Returns ok=false when the expression's type can't be determined via this
// heuristic (not a call/ident, or resolves outside the target module).
func resolveExprReturnType(expr ast.Expr, aliasMap map[string]string, registry ReturnTypeRegistry, localTypes map[string]returnType) (returnType, bool) {
	switch e := expr.(type) {
	case *ast.Ident:
		rt, ok := localTypes[e.Name]
		return rt, ok

	case *ast.CallExpr:
		sel, ok := e.Fun.(*ast.SelectorExpr)
		if !ok {
			return returnType{}, false
		}
		// Direct constructor call: pkg.NewX(...) where X.Sel is a package alias.
		if pkgIdent, ok := sel.X.(*ast.Ident); ok {
			if importPath, isTarget := aliasMap[pkgIdent.Name]; isTarget {
				if rt, found := registry.Funcs[importPath+"."+sel.Sel.Name]; found {
					return rt, true
				}
				// pkgIdent matched an import alias but not a known constructor —
				// don't fall through to treating it as a local var too.
				return returnType{}, false
			}
		}
		// Method/chain call: <expr>.Method(...) — resolve <expr>'s type first.
		recvType, ok := resolveExprReturnType(sel.X, aliasMap, registry, localTypes)
		if !ok {
			return returnType{}, false
		}
		if rt, found := registry.Methods[recvType.PkgPath+"."+recvType.TypeName+"."+sel.Sel.Name]; found {
			return rt, true
		}
		return returnType{}, false

	default:
		return returnType{}, false
	}
}

// scanReturnTypeCallSites walks each top-level function declaration in f,
// tracking local variables assigned from a target-module constructor/method
// call, and reports every call to methodName made on a value whose type
// resolves (via aliasMap + registry + the function's own assignments) to a
// target-module type. Covers both `v := pkg.NewX(); v.M()` and fluent chains
// `pkg.NewX().M1().M2()` with no intermediate variable — both route through
// resolveExprReturnType. Scope is strictly same-function: each function body
// (including nested function literals) gets its own fresh local-variable index.
func scanReturnTypeCallSites(f *ast.File, fset *token.FileSet, repoDir string, aliasMap map[string]string, registry ReturnTypeRegistry, methodName, typeQualifier string) []CallSite {
	var sites []CallSite

	var walkBody func(body *ast.BlockStmt)
	walkBody = func(body *ast.BlockStmt) {
		if body == nil {
			return
		}
		localTypes := make(map[string]returnType)
		ast.Inspect(body, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.FuncLit:
				// Nested function literal gets its own scope — its
				// assignments must not leak into the enclosing function's
				// index, and vice versa.
				walkBody(node.Body)
				return false

			case *ast.AssignStmt:
				assignPairs(node, func(lhsIdent *ast.Ident, rhs ast.Expr) {
					call, ok := rhs.(*ast.CallExpr)
					if !ok {
						delete(localTypes, lhsIdent.Name)
						return
					}
					if rt, ok := resolveExprReturnType(call, aliasMap, registry, localTypes); ok {
						localTypes[lhsIdent.Name] = rt
					} else {
						delete(localTypes, lhsIdent.Name)
					}
				})
				return true

			case *ast.CallExpr:
				sel, ok := node.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != methodName {
					return true
				}
				rt, ok := resolveExprReturnType(sel.X, aliasMap, registry, localTypes)
				if !ok {
					return true
				}
				if typeQualifier != "" && !matchesQualifier(rt.PkgPath, rt.TypeName, typeQualifier) {
					return true
				}
				pos := fset.Position(node.Pos())
				relPath, relErr := filepath.Rel(repoDir, pos.Filename)
				if relErr != nil {
					relPath = pos.Filename
				}
				viaVar := ""
				if ident, isIdent := sel.X.(*ast.Ident); isIdent {
					if _, known := localTypes[ident.Name]; known {
						viaVar = ident.Name
					}
				}
				sites = append(sites, CallSite{
					File:            filepath.ToSlash(relPath),
					Line:            pos.Line,
					Column:          pos.Column,
					FuncName:        rt.PkgPath + "." + methodName,
					RawName:         exprToStringShallow(sel.X) + "." + methodName,
					ArgCount:        len(node.Args),
					ViaLocalVar:     viaVar,
					ViaLocalVarType: rt.PkgPath + "." + rt.TypeName,
				})
			}
			return true
		})
	}

	ast.Inspect(f, func(n ast.Node) bool {
		if fn, ok := n.(*ast.FuncDecl); ok {
			walkBody(fn.Body)
			return false
		}
		return true
	})

	return sites
}

// assignPairs invokes fn for each (identifier, rhs-expression) pair in an
// AssignStmt, handling both `v := f()` / `v, err := f()` (single call,
// possibly multi-value — only the first lhs is paired with the call) and
// `a, b := f(), g()` (parallel single-value assignment). The blank
// identifier `_` is skipped.
func assignPairs(node *ast.AssignStmt, fn func(lhsIdent *ast.Ident, rhs ast.Expr)) {
	if len(node.Rhs) == 1 && len(node.Lhs) >= 1 {
		if lhsIdent, ok := node.Lhs[0].(*ast.Ident); ok && lhsIdent.Name != "_" {
			fn(lhsIdent, node.Rhs[0])
		}
		return
	}
	for i, rhs := range node.Rhs {
		if i >= len(node.Lhs) {
			break
		}
		lhsIdent, ok := node.Lhs[i].(*ast.Ident)
		if !ok || lhsIdent.Name == "_" {
			continue
		}
		fn(lhsIdent, rhs)
	}
}

// exprToStringShallow renders a simple expression (Ident or CallExpr chain)
// for RawName purposes without pulling in the full go/printer machinery.
func exprToStringShallow(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.CallExpr:
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok {
			return exprToStringShallow(sel.X) + "." + sel.Sel.Name + "(...)"
		}
	}
	return "?"
}

// splitQualifiedName splits "helper.Must" into ("helper", "Must")
// and "Must" into ("", "Must").
func splitQualifiedName(name string) (pkg, fn string) {
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[:idx], name[idx+1:]
	}
	return "", name
}

// SplitQualifiedName is the public version of splitQualifiedName.
func SplitQualifiedName(name string) (pkg, fn string) {
	return splitQualifiedName(name)
}
