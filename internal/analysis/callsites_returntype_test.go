package analysis

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func parseTestFile(t *testing.T, src string) (*ast.File, *token.FileSet) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	return f, fset
}

func testRegistry() ReturnTypeRegistry {
	pkg := "example.com/lib/pipeline"
	return ReturnTypeRegistry{
		Funcs: map[string]returnType{
			pkg + ".NewPipelineBuilder": {PkgPath: pkg, TypeName: "PipelineBuilder"},
		},
		Methods: map[string]returnType{
			pkg + ".PipelineBuilder.Group": {PkgPath: pkg, TypeName: "PipelineBuilder"},
			pkg + ".PipelineBuilder.Match": {PkgPath: pkg, TypeName: "PipelineBuilder"},
		},
	}
}

func TestScanReturnTypeCallSites_LocalVar(t *testing.T) {
	src := `package main

import "example.com/lib/pipeline"

func run() {
	pipe := pipeline.NewPipelineBuilder()
	pipe.Group("x")
}
`
	f, fset := parseTestFile(t, src)
	aliasMap := map[string]string{"pipeline": "example.com/lib/pipeline"}
	sites := scanReturnTypeCallSites(f, fset, "", aliasMap, testRegistry(), "Group", "")
	if len(sites) != 1 {
		t.Fatalf("got %d sites, want 1: %+v", len(sites), sites)
	}
	if sites[0].ViaLocalVar != "pipe" || sites[0].ViaLocalVarType != "example.com/lib/pipeline.PipelineBuilder" {
		t.Errorf("site = %+v", sites[0])
	}
}

func TestScanReturnTypeCallSites_Chain(t *testing.T) {
	src := `package main

import "example.com/lib/pipeline"

func run() {
	pipeline.NewPipelineBuilder().Group("x").Match("y")
}
`
	f, fset := parseTestFile(t, src)
	aliasMap := map[string]string{"pipeline": "example.com/lib/pipeline"}
	sitesGroup := scanReturnTypeCallSites(f, fset, "", aliasMap, testRegistry(), "Group", "")
	sitesMatch := scanReturnTypeCallSites(f, fset, "", aliasMap, testRegistry(), "Match", "")
	if len(sitesGroup) != 1 {
		t.Fatalf("Group: got %d sites, want 1", len(sitesGroup))
	}
	if len(sitesMatch) != 1 {
		t.Fatalf("Match: got %d sites, want 1", len(sitesMatch))
	}
	if sitesGroup[0].ViaLocalVar != "" {
		t.Errorf("chain call should have empty ViaLocalVar, got %q", sitesGroup[0].ViaLocalVar)
	}
	if sitesGroup[0].ViaLocalVarType != "example.com/lib/pipeline.PipelineBuilder" {
		t.Errorf("Group ViaLocalVarType = %q", sitesGroup[0].ViaLocalVarType)
	}
}

func TestScanReturnTypeCallSites_ChainBreaksOnNonTargetType(t *testing.T) {
	src := `package main

import "example.com/lib/pipeline"

func run() {
	pipeline.NewPipelineBuilder().Build().SomeMethod()
}
`
	f, fset := parseTestFile(t, src)
	aliasMap := map[string]string{"pipeline": "example.com/lib/pipeline"}
	// Build() returns string, not a target type, and is not in the test
	// registry — resolveExprReturnType can't continue past it, so a search
	// for SomeMethod (called on Build()'s return value) must find nothing:
	// the chain is genuinely broken at that point, not merely "the searched
	// method isn't chain-capable."
	sites := scanReturnTypeCallSites(f, fset, "", aliasMap, testRegistry(), "SomeMethod", "")
	if len(sites) != 0 {
		t.Fatalf("chain should break at Build() (unregistered, non-target return type); got %d sites: %+v", len(sites), sites)
	}
}

func TestScanReturnTypeCallSites_TerminalMethodNotInRegistry(t *testing.T) {
	src := `package main

import "example.com/lib/pipeline"

func run() {
	pipe := pipeline.NewPipelineBuilder()
	pipe.Build()
}
`
	f, fset := parseTestFile(t, src)
	aliasMap := map[string]string{"pipeline": "example.com/lib/pipeline"}
	// Build is deliberately not in testRegistry().Methods (it returns string,
	// not a target type) — but pipe's OWN type (PipelineBuilder) is known, so
	// a direct call to any method name on pipe must still be detected,
	// matching scanDICallSites/scanParamCallSites parity (they don't require
	// the called method to be independently registered either).
	sites := scanReturnTypeCallSites(f, fset, "", aliasMap, testRegistry(), "Build", "")
	if len(sites) != 1 {
		t.Fatalf("terminal call to a non-chain-capable method on a known local var should be detected; got %d sites: %+v", len(sites), sites)
	}
	if sites[0].ViaLocalVar != "pipe" {
		t.Errorf("site = %+v", sites[0])
	}
}

func TestScanReturnTypeCallSites_Reassignment(t *testing.T) {
	src := `package main

import "example.com/lib/pipeline"

func run() {
	pipe := pipeline.NewPipelineBuilder()
	pipe.Group("x")
	pipe = nil
	pipe.Group("y")
}
`
	f, fset := parseTestFile(t, src)
	aliasMap := map[string]string{"pipeline": "example.com/lib/pipeline"}
	sites := scanReturnTypeCallSites(f, fset, "", aliasMap, testRegistry(), "Group", "")
	if len(sites) != 1 {
		t.Fatalf("reassignment to a non-target value should drop the var from the index; got %d sites, want 1: %+v", len(sites), sites)
	}
	if sites[0].Line != 7 {
		t.Errorf("expected the site before reassignment (line 7), got line %d", sites[0].Line)
	}
}

func TestScanReturnTypeCallSites_MultiReturnConstructor(t *testing.T) {
	src := `package main

import "example.com/lib/pipeline"

func run() {
	pipe, err := pipeline.NewPipelineBuilderWithError()
	if err != nil {
		return
	}
	pipe.Group("x")
}
`
	f, fset := parseTestFile(t, src)
	aliasMap := map[string]string{"pipeline": "example.com/lib/pipeline"}
	reg := testRegistry()
	reg.Funcs["example.com/lib/pipeline.NewPipelineBuilderWithError"] = returnType{PkgPath: "example.com/lib/pipeline", TypeName: "PipelineBuilder"}
	sites := scanReturnTypeCallSites(f, fset, "", aliasMap, reg, "Group", "")
	if len(sites) != 1 {
		t.Fatalf("(T, error) constructor assigned via v, err := ...; v.Method() should be detected; got %d sites: %+v", len(sites), sites)
	}
	if sites[0].ViaLocalVar != "pipe" {
		t.Errorf("site = %+v", sites[0])
	}
}

func TestScanReturnTypeCallSites_SameFunctionScopeOnly(t *testing.T) {
	src := `package main

import "example.com/lib/pipeline"

func makeIt() {
	pipe := pipeline.NewPipelineBuilder()
	pipe.Group("x")
}

func useIt() {
	pipe.Group("y")
}
`
	f, fset := parseTestFile(t, src)
	aliasMap := map[string]string{"pipeline": "example.com/lib/pipeline"}
	sites := scanReturnTypeCallSites(f, fset, "", aliasMap, testRegistry(), "Group", "")
	if len(sites) != 1 {
		t.Fatalf("var index must not leak across functions; got %d sites, want 1: %+v", len(sites), sites)
	}
}
