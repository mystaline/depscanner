package analysis

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildReturnTypeRegistry(t *testing.T) {
	dir := t.TempDir()
	src := `package pipeline

type PipelineBuilder struct{}

func NewPipelineBuilder() *PipelineBuilder { return &PipelineBuilder{} }

func (pb *PipelineBuilder) Group(data string) *PipelineBuilder { return pb }

func (pb *PipelineBuilder) Build() string { return "" }

func NewThingWithError() (*PipelineBuilder, error) { return nil, nil }
`
	if err := os.WriteFile(filepath.Join(dir, "pipeline.go"), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	idx, err := BuildSymbolIndex(dir, "example.com/lib/pipeline")
	if err != nil {
		t.Fatal(err)
	}

	reg := BuildReturnTypeRegistry(idx)

	rt, ok := reg.Funcs["example.com/lib/pipeline.NewPipelineBuilder"]
	if !ok {
		t.Fatal("NewPipelineBuilder not found in Funcs")
	}
	if rt.PkgPath != "example.com/lib/pipeline" || rt.TypeName != "PipelineBuilder" {
		t.Errorf("NewPipelineBuilder returnType = %+v", rt)
	}

	rt, ok = reg.Methods["example.com/lib/pipeline.PipelineBuilder.Group"]
	if !ok {
		t.Fatal("PipelineBuilder.Group not found in Methods")
	}
	if rt.PkgPath != "example.com/lib/pipeline" || rt.TypeName != "PipelineBuilder" {
		t.Errorf("Group returnType = %+v", rt)
	}

	if _, ok := reg.Methods["example.com/lib/pipeline.PipelineBuilder.Build"]; ok {
		t.Error("Build returns string, should not be in registry")
	}

	rt, ok = reg.Funcs["example.com/lib/pipeline.NewThingWithError"]
	if !ok {
		t.Fatal("NewThingWithError (T, error) shape not found in Funcs")
	}
	if rt.TypeName != "PipelineBuilder" {
		t.Errorf("NewThingWithError returnType = %+v", rt)
	}
}
