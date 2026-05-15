package analysis

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// ---- DI field detection helpers ----

func writeTempFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func mustScan(t *testing.T, dir, module, symbol string) []CallSite {
	t.Helper()
	sites, warnings, err := ScanSymbolReferences(dir, module, symbol)
	if err != nil {
		t.Fatalf("ScanSymbolReferences(%q): %v", symbol, err)
	}
	for _, w := range warnings {
		t.Logf("warn: %s", w)
	}
	return sites
}

func assertSiteCount(t *testing.T, sites []CallSite, want int, label string) {
	t.Helper()
	if len(sites) != want {
		t.Errorf("%s: got %d sites, want %d", label, len(sites), want)
		for _, s := range sites {
			t.Logf("  %s:%d %s via=%q", s.File, s.Line, s.RawName, s.ViaField)
		}
	}
}

func assertHasDI(t *testing.T, sites []CallSite, fieldName, methodName string) {
	t.Helper()
	for _, s := range sites {
		if s.ViaField == fieldName && s.RawName == fieldName+"."+methodName {
			return
		}
	}
	t.Errorf("no DI site found with ViaField=%q RawName=%q", fieldName, fieldName+"."+methodName)
	for _, s := range sites {
		t.Logf("  have: %s:%d raw=%q via=%q", s.File, s.Line, s.RawName, s.ViaField)
	}
}

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

func TestScanSymbolReferences(t *testing.T) {
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
			sites, warnings, err := ScanSymbolReferences(dir, target, tt.funcName)
			if err != nil {
				t.Fatalf("ScanSymbolReferences(%q): %v", tt.funcName, err)
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

// ---- DI method call detection tests ----

// TestDISameFile: struct declaration and DI method call are in the same file.
func TestDISameFile(t *testing.T) {
	dir := t.TempDir()
	const module = "example.com/org/lib"
	writeTempFile(t, dir, "usecase.go", `package usecase

import svc "example.com/org/lib/service"

type CreateUsecase struct {
	SchedulerService svc.SchedulerService
}

func (u *CreateUsecase) Execute() {
	u.SchedulerService.Schedule("job", nil, nil)
	u.SchedulerService.Schedule("job2", nil, nil)
}
`)
	sites := mustScan(t, dir, module, "Schedule")
	assertSiteCount(t, sites, 2, "Schedule via DI field")
	for _, s := range sites {
		assertHasDI(t, []CallSite{s}, "SchedulerService", "Schedule")
		if s.ViaFieldType != "example.com/org/lib/service.SchedulerService" {
			t.Errorf("ViaFieldType = %q, want %q", s.ViaFieldType, "example.com/org/lib/service.SchedulerService")
		}
	}
}

// TestDISiblingFile: struct defined in one file, DI call in another file of the same package.
func TestDISiblingFile(t *testing.T) {
	dir := t.TempDir()
	const module = "example.com/org/lib"
	// File 1: struct declaration (imports target module)
	writeTempFile(t, dir, "types.go", `package usecase

import svc "example.com/org/lib/service"

type Handler struct {
	emailService svc.EmailService
}
`)
	// File 2: method implementation (does NOT import target module directly)
	writeTempFile(t, dir, "handler.go", `package usecase

func (h *Handler) Notify(msg string) {
	h.emailService.Send("admin@example.com", msg)
}
`)
	sites := mustScan(t, dir, module, "Send")
	assertSiteCount(t, sites, 1, "Send via sibling file DI")
	assertHasDI(t, sites, "emailService", "Send")
}

// TestDIPointerField: field type is a pointer to a target module type.
func TestDIPointerField(t *testing.T) {
	dir := t.TempDir()
	const module = "example.com/org/lib"
	writeTempFile(t, dir, "service.go", `package app

import db "example.com/org/lib/db"

type App struct {
	database *db.DatabaseService
}

func (a *App) Query() {
	a.database.Exec("SELECT 1")
}
`)
	sites := mustScan(t, dir, module, "Exec")
	assertSiteCount(t, sites, 1, "Exec via pointer DI field")
	assertHasDI(t, sites, "database", "Exec")
}

// TestDIUnexportedField: lowercase (unexported) DI field is also detected.
func TestDIUnexportedField(t *testing.T) {
	dir := t.TempDir()
	const module = "example.com/org/lib"
	writeTempFile(t, dir, "worker.go", `package worker

import kafka "example.com/org/lib/kafka"

type Worker struct {
	kafkaService kafka.KafkaService
}

func (w *Worker) Publish(topic string, data []byte) {
	w.kafkaService.Publish(topic, data)
}
`)
	sites := mustScan(t, dir, module, "Publish")
	assertSiteCount(t, sites, 1, "Publish via unexported DI field")
	assertHasDI(t, sites, "kafkaService", "Publish")
}

// TestDIQualifiedSymbol: "SchedulerService.Schedule" only matches fields of type SchedulerService,
// not fields of a different type (EmailService) that also has a method with the same name.
func TestDIQualifiedSymbol(t *testing.T) {
	dir := t.TempDir()
	const module = "example.com/org/lib"
	writeTempFile(t, dir, "multi.go", `package app

import svc "example.com/org/lib/service"

type MultiService struct {
	SchedulerService svc.SchedulerService
	emailService  svc.EmailService // different type, also has a Schedule method
}

func (m *MultiService) Run() {
	m.SchedulerService.Schedule("a", nil, nil) // should match (type = SchedulerService)
	m.emailService.Schedule("b", nil, nil)  // should NOT match (type = EmailService)
}
`)
	sites := mustScan(t, dir, module, "SchedulerService.Schedule")
	assertSiteCount(t, sites, 1, "qualified SchedulerService.Schedule")
	assertHasDI(t, sites, "SchedulerService", "Schedule")
	if sites[0].ViaFieldType != "example.com/org/lib/service.SchedulerService" {
		t.Errorf("ViaFieldType = %q, want service.SchedulerService", sites[0].ViaFieldType)
	}
}

// TestDIMultipleFields: struct with multiple DI fields — each field's calls are detected.
func TestDIMultipleFields(t *testing.T) {
	dir := t.TempDir()
	const module = "example.com/org/lib"
	writeTempFile(t, dir, "usecase.go", `package usecase

import svc "example.com/org/lib/service"

type TxUsecase struct {
	SchedulerService   svc.SchedulerService
	TemplateService svc.TemplateService
}

func (u *TxUsecase) Execute() {
	u.SchedulerService.Schedule("job", nil, nil)
	u.TemplateService.Render("tmpl", nil)
}
`)
	schedSites := mustScan(t, dir, module, "Schedule")
	assertSiteCount(t, schedSites, 1, "Schedule")
	assertHasDI(t, schedSites, "SchedulerService", "Schedule")

	renderSites := mustScan(t, dir, module, "Render")
	assertSiteCount(t, renderSites, 1, "Render")
	assertHasDI(t, renderSites, "TemplateService", "Render")
}

// TestDINoFalsePositiveLocalVar: a method call on a local variable should NOT be
// detected as a DI call (no struct field involved).
func TestDINoFalsePositiveLocalVar(t *testing.T) {
	dir := t.TempDir()
	const module = "example.com/org/lib"
	// This file imports target module and has a local var call, not a field call.
	writeTempFile(t, dir, "handler.go", `package handler

import svc "example.com/org/lib/service"

func Process(agenda svc.SchedulerService) {
	agenda.Schedule("job", nil, nil) // local param, not a struct field
}
`)
	sites := mustScan(t, dir, module, "Schedule")
	// agenda.Schedule is a direct call (single-level selector), handled by existing logic.
	// It should NOT appear as a DI call (ViaField should be empty for that site).
	for _, s := range sites {
		if s.ViaField != "" {
			t.Errorf("expected ViaField=\"\" for local var call, got %q", s.ViaField)
		}
	}
}

// TestDINoFalsePositiveUnknownField: a chained call where the field is NOT a target
// module type should not be detected.
func TestDINoFalsePositiveUnknownField(t *testing.T) {
	dir := t.TempDir()
	const module = "example.com/org/lib"
	writeTempFile(t, dir, "handler.go", `package handler

import svc "example.com/org/lib/service"

type Handler struct {
	realService svc.SchedulerService
	otherThing  SomeLocalType // NOT from target module
}

type SomeLocalType struct{}
func (s *SomeLocalType) Schedule(name string) {}

func (h *Handler) Run() {
	h.otherThing.Schedule("x") // should NOT be detected as DI from target module
}
`)
	sites := mustScan(t, dir, module, "Schedule")
	for _, s := range sites {
		if s.ViaField == "otherThing" {
			t.Errorf("false positive: otherThing.Schedule was detected as DI from target module")
		}
	}
}

// TestDIArgCount: DI call sites correctly capture argument count.
func TestDIArgCount(t *testing.T) {
	dir := t.TempDir()
	const module = "example.com/org/lib"
	writeTempFile(t, dir, "svc.go", `package app

import redis "example.com/org/lib/redis"

type Cache struct {
	redisService redis.RedisService
}

func (c *Cache) Store() {
	c.redisService.Set("key", "value", 300)
}
`)
	sites := mustScan(t, dir, module, "Set")
	assertSiteCount(t, sites, 1, "Set")
	if sites[0].ArgCount != 3 {
		t.Errorf("ArgCount = %d, want 3", sites[0].ArgCount)
	}
}

// TestExtractDIFields_Basic: unit test for extractDIFields helper.
func TestExtractDIFields_Basic(t *testing.T) {
	src := `package p
import svc "example.com/org/lib/service"
type MyStruct struct {
	Agenda     svc.SchedulerService
	ptrField   *svc.EmailService
	notFromLib string
}
`
	f, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	aliasMap := map[string]string{"svc": "example.com/org/lib/service"}
	index := make(map[string][]diFieldEntry)
	extractDIFields(f, aliasMap, index)

	if _, ok := index["Agenda"]; !ok {
		t.Error("Agenda field not indexed")
	}
	if _, ok := index["ptrField"]; !ok {
		t.Error("ptrField (pointer type) not indexed")
	}
	if _, ok := index["notFromLib"]; ok {
		t.Error("notFromLib should not be indexed")
	}
	if index["Agenda"][0].typeName != "SchedulerService" {
		t.Errorf("Agenda typeName = %q, want SchedulerService", index["Agenda"][0].typeName)
	}
	if index["ptrField"][0].typeName != "EmailService" {
		t.Errorf("ptrField typeName = %q, want EmailService", index["ptrField"][0].typeName)
	}
}

// TestExtractDIFields_SliceField: slice field types are indexed.
func TestExtractDIFields_SliceField(t *testing.T) {
	src := `package p
import svc "example.com/org/lib/service"
type MyStruct struct {
	workers    []svc.WorkerService
	ptrWorkers []*svc.WorkerService
}
`
	f, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	aliasMap := map[string]string{"svc": "example.com/org/lib/service"}
	index := make(map[string][]diFieldEntry)
	extractDIFields(f, aliasMap, index)

	if _, ok := index["workers"]; !ok {
		t.Error("workers ([]T) field not indexed")
	}
	if _, ok := index["ptrWorkers"]; !ok {
		t.Error("ptrWorkers ([]*T) field not indexed")
	}
	if index["workers"][0].typeName != "WorkerService" {
		t.Errorf("workers typeName = %q, want WorkerService", index["workers"][0].typeName)
	}
}

// TestExtractDIFields_DotImport: dot-imported type fields are indexed.
func TestExtractDIFields_DotImport(t *testing.T) {
	src := `package p
import . "example.com/org/lib/service"
type MyStruct struct {
	agenda SchedulerService
}
`
	f, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	aliasMap := map[string]string{".": "example.com/org/lib/service"}
	index := make(map[string][]diFieldEntry)
	extractDIFields(f, aliasMap, index)

	if _, ok := index["agenda"]; !ok {
		t.Error("agenda (dot-import) field not indexed")
	}
	if index["agenda"][0].typeName != "SchedulerService" {
		t.Errorf("agenda typeName = %q, want SchedulerService", index["agenda"][0].typeName)
	}
}

// TestDIPackageQualified: searching with a package qualifier like "service.Schedule"
// (not a type qualifier like "SchedulerService.Schedule") must also match DI calls.
func TestDIPackageQualified(t *testing.T) {
	dir := t.TempDir()
	const module = "example.com/org/lib"
	writeTempFile(t, dir, "usecase.go", `package usecase

import svc "example.com/org/lib/service"

type CreateUsecase struct {
	SchedulerService svc.SchedulerService
}

func (u *CreateUsecase) Execute() {
	u.SchedulerService.Schedule("job", nil, nil)
}
`)
	// "service.Schedule" — qualPkg="service" matches import path suffix "service".
	sites := mustScan(t, dir, module, "service.Schedule")
	assertSiteCount(t, sites, 1, "package-qualified service.Schedule")
	assertHasDI(t, sites, "SchedulerService", "Schedule")
}

// TestDISliceField: method call on a slice-type DI field element is detected.
func TestDISliceField(t *testing.T) {
	dir := t.TempDir()
	const module = "example.com/org/lib"
	writeTempFile(t, dir, "svc.go", `package app

import svc "example.com/org/lib/service"

type Dispatcher struct {
	workers []svc.WorkerService
}

func (d *Dispatcher) Run(i int) {
	d.workers[i].Process("task")
}
`)
	// Note: d.workers[i].Process is a 3-level chain (workers[i] = IndexExpr).
	// This specific call is NOT detected by DI scanning (only 2-level obj.field.method is supported).
	// The test verifies no false positive — we get 0 sites, not a crash.
	sites := mustScan(t, dir, module, "Process")
	for _, s := range sites {
		if s.ViaField == "workers" {
			t.Errorf("false positive: indexed-slice call workers[i].Process should not be detected as 2-level DI")
		}
	}
}

// TestDICompositeQualifier: impact command builds targets like "service.SchedulerService.Schedule"
// which produces qualPkg="service.SchedulerService". This composite qualifier must match DI fields
// of type SchedulerService from the service package.
func TestDICompositeQualifier(t *testing.T) {
	dir := t.TempDir()
	const module = "example.com/org/lib"
	writeTempFile(t, dir, "usecase.go", `package usecase

import svc "example.com/org/lib/service"

type CreateUsecase struct {
	agendaService svc.SchedulerService
}

func (u *CreateUsecase) Execute() {
	u.agendaService.Schedule("job", nil, nil)
}
`)
	// Simulate what impact command sends: relPkg="service", symName="SchedulerService.Schedule"
	// → target = "service.SchedulerService.Schedule"
	// splitQualifiedName → qualPkg="service.SchedulerService", plainFunc="Schedule"
	sites := mustScan(t, dir, module, "service.SchedulerService.Schedule")
	assertSiteCount(t, sites, 1, "composite qualifier service.SchedulerService.Schedule")
	assertHasDI(t, sites, "agendaService", "Schedule")
}

// TestDIFuncNameFormat: DI call sites must have FuncName = "importPath.method" (not
// "importPath.TypeName.method") so that AnalyzeImpact's matchesCallSite works correctly.
func TestDIFuncNameFormat(t *testing.T) {
	dir := t.TempDir()
	const module = "example.com/org/lib"
	writeTempFile(t, dir, "usecase.go", `package usecase

import svc "example.com/org/lib/service"

type CreateUsecase struct {
	agendaService svc.SchedulerService
}

func (u *CreateUsecase) Execute() {
	u.agendaService.Schedule("job", nil, nil)
}
`)
	sites := mustScan(t, dir, module, "Schedule")
	assertSiteCount(t, sites, 1, "Schedule")
	// FuncName must be pkg.Method — not pkg.TypeName.Method
	want := "example.com/org/lib/service.Schedule"
	if sites[0].FuncName != want {
		t.Errorf("FuncName = %q, want %q", sites[0].FuncName, want)
	}
	// Type info is still available in ViaFieldType
	wantType := "example.com/org/lib/service.SchedulerService"
	if sites[0].ViaFieldType != wantType {
		t.Errorf("ViaFieldType = %q, want %q", sites[0].ViaFieldType, wantType)
	}
}

// TestDIAnalyzeImpactIntegration: DI-detected call sites are matched by AnalyzeImpact.
// This is the end-to-end test for the impact command path.
func TestDIAnalyzeImpactIntegration(t *testing.T) {
	dir := t.TempDir()
	const module = "example.com/org/lib"
	writeTempFile(t, dir, "usecase.go", `package usecase

import svc "example.com/org/lib/service"

type CreateUsecase struct {
	agendaService svc.SchedulerService
}

func (u *CreateUsecase) Execute() {
	u.agendaService.Schedule("job", nil, nil)
}
`)
	// Simulate what impact command does:
	// SplitSymbolKey("example.com/org/lib/service.SchedulerService.Schedule")
	// → pkg="example.com/org/lib/service", name="SchedulerService.Schedule"
	// target = "service.SchedulerService.Schedule"
	sites, _, err := ScanSymbolReferences(dir, module, "service.SchedulerService.Schedule")
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) == 0 {
		t.Fatal("no sites found — DI detection failed")
	}

	// Simulate AnalyzeImpact: build a synthetic breaking change and verify it matches.
	change := SymbolChange{
		Symbol:   "example.com/org/lib/service.SchedulerService.Schedule",
		Kind:     ChangeSignature,
		Breaking: true,
		Category: KindMethod,
	}
	repoCallSites := map[string][]CallSite{"my-service": sites}
	impacts := AnalyzeImpact([]SymbolChange{change}, repoCallSites)

	if len(impacts) == 0 {
		t.Error("AnalyzeImpact found no impacts — FuncName format mismatch")
		for _, s := range sites {
			t.Logf("  site FuncName=%q ViaField=%q", s.FuncName, s.ViaField)
		}
		return
	}
	if impacts[0].TotalSites != 1 {
		t.Errorf("TotalSites = %d, want 1", impacts[0].TotalSites)
	}
}

// TestDIDeduplication: the same call site is not reported twice even if detected by
// both the direct-call heuristic and the DI scanner.
func TestDIDeduplication(t *testing.T) {
	dir := t.TempDir()
	const module = "example.com/org/lib"
	writeTempFile(t, dir, "usecase.go", `package usecase

import svc "example.com/org/lib/service"

type U struct {
	SchedulerService svc.SchedulerService
}

func (u *U) Run() {
	u.SchedulerService.Schedule("a", nil, nil)
}
`)
	sites := mustScan(t, dir, module, "Schedule")
	// Regardless of how many internal passes fire, each unique file:line:col
	// should appear exactly once.
	seen := make(map[string]int)
	for _, s := range sites {
		key := fmt.Sprintf("%s:%d:%d", s.File, s.Line, s.Column)
		seen[key]++
		if seen[key] > 1 {
			t.Errorf("duplicate site at %s", key)
		}
	}
}
