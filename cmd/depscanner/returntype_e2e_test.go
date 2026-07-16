// cmd/depscanner/returntype_e2e_test.go
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mystaline/depscanner/internal/analysis"
)

func gitInitReturnType(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
		{"add", "-A"},
		{"commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestReturnTypeE2E_LocalVarAndChain(t *testing.T) {
	root := t.TempDir()

	sourceDir := filepath.Join(root, "pipeline")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sourceSrc := `package pipeline

type PipelineBuilder struct{}

func NewPipelineBuilder() *PipelineBuilder { return &PipelineBuilder{} }

func (pb *PipelineBuilder) Group(data string) *PipelineBuilder { return pb }

func (pb *PipelineBuilder) Match(data string) *PipelineBuilder { return pb }
`
	if err := os.WriteFile(filepath.Join(sourceDir, "go.mod"), []byte("module example.com/org/pipeline\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "pipeline.go"), []byte(sourceSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	consumerDir := filepath.Join(root, "consumer")
	gitInitReturnType(t, consumerDir, map[string]string{
		"go.mod": "module consumer/svc\n\ngo 1.22\n\nrequire example.com/org/pipeline v0.1.0\n",
		"main.go": `package main

import "example.com/org/pipeline"

func viaLocalVar() {
	pipe := pipeline.NewPipelineBuilder()
	pipe.Group("x")
}

func viaChain() {
	pipeline.NewPipelineBuilder().Group("x").Match("y")
}
`,
	})

	idx, err := analysis.BuildSymbolIndex(sourceDir, "example.com/org/pipeline")
	if err != nil {
		t.Fatal(err)
	}
	registry := analysis.BuildReturnTypeRegistry(idx)

	groupSites, warnings, err := analysis.ScanSymbolReferences(consumerDir, "example.com/org/pipeline", "PipelineBuilder.Group", registry)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range warnings {
		t.Logf("warn: %s", w)
	}
	if len(groupSites) != 2 {
		t.Fatalf("Group: got %d sites, want 2 (one via local var, one via chain): %+v", len(groupSites), groupSites)
	}

	matchSites, _, err := analysis.ScanSymbolReferences(consumerDir, "example.com/org/pipeline", "PipelineBuilder.Match", registry)
	if err != nil {
		t.Fatal(err)
	}
	if len(matchSites) != 1 {
		t.Fatalf("Match: got %d sites, want 1 (chain only): %+v", len(matchSites), matchSites)
	}
	if matchSites[0].ViaLocalVar != "" {
		t.Errorf("chain-only Match call should have no ViaLocalVar, got %q", matchSites[0].ViaLocalVar)
	}

	// Regression guard: the zero-value registry (pre-Phase-A behavior) must
	// still find nothing for this pattern — proves the fix is additive, not
	// a change to unrelated matching paths.
	zeroRegistrySites, _, err := analysis.ScanSymbolReferences(consumerDir, "example.com/org/pipeline", "PipelineBuilder.Group", analysis.ReturnTypeRegistry{})
	if err != nil {
		t.Fatal(err)
	}
	if len(zeroRegistrySites) != 0 {
		t.Fatalf("with zero-value registry, expected 0 sites (pre-Phase-A baseline), got %d: %+v", len(zeroRegistrySites), zeroRegistrySites)
	}
}
