package analysis

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseGoMod(t *testing.T) {
	tests := []struct {
		name          string
		gomodContent  string
		targetModule  string
		expectFound   bool
		expectVersion string
	}{
		{
			name: "single line require",
			gomodContent: `module github.com/example/app

go 1.22

require github.com/mystaline/depscanner v0.2.0
`,
			targetModule:  "github.com/mystaline/depscanner",
			expectFound:   true,
			expectVersion: "v0.2.0",
		},
		{
			name: "require block",
			gomodContent: `module github.com/example/app

go 1.22

require (
	github.com/spf13/cobra v1.10.2
	github.com/mystaline/depscanner v1.0.0-20260409024228-abc123def456
	gopkg.in/yaml.v3 v3.0.1
)
`,
			targetModule:  "github.com/mystaline/depscanner",
			expectFound:   true,
			expectVersion: "v1.0.0-20260409024228-abc123def456",
		},
		{
			name: "require block with comments",
			gomodContent: `module github.com/example/app

require (
	// dependency comment
	github.com/mystaline/depscanner v0.1.0 // inline comment
)
`,
			targetModule:  "github.com/mystaline/depscanner",
			expectFound:   true,
			expectVersion: "v0.1.0",
		},
		{
			name: "module not found",
			gomodContent: `module github.com/example/app

go 1.22

require github.com/other/lib v1.0.0
`,
			targetModule:  "github.com/mystaline/depscanner",
			expectFound:   false,
			expectVersion: "",
		},
		{
			name: "empty require block",
			gomodContent: `module github.com/example/app

require (
)
`,
			targetModule:  "github.com/mystaline/depscanner",
			expectFound:   false,
			expectVersion: "",
		},
		{
			name: "mixed single and block require",
			gomodContent: `module github.com/example/app

require github.com/foo v1.0.0

require (
	github.com/mystaline/depscanner v0.3.0
	github.com/bar v2.0.0
)
`,
			targetModule:  "github.com/mystaline/depscanner",
			expectFound:   true,
			expectVersion: "v0.3.0",
		},
		{
			name: "pseudo-version",
			gomodContent: `module github.com/example/app

require github.com/mystaline/depscanner v0.0.0-20260311025516-abcdef123456
`,
			targetModule:  "github.com/mystaline/depscanner",
			expectFound:   true,
			expectVersion: "v0.0.0-20260311025516-abcdef123456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary go.mod file
			tmpdir := t.TempDir()
			gomodPath := filepath.Join(tmpdir, "go.mod")
			err := os.WriteFile(gomodPath, []byte(tt.gomodContent), 0o644)
			if err != nil {
				t.Fatalf("failed to create temp go.mod: %v", err)
			}

			got, err := ParseGoMod(gomodPath, tt.targetModule)
			if err != nil {
				t.Fatalf("ParseGoMod failed: %v", err)
			}

			if got.Found != tt.expectFound {
				t.Errorf("Found = %v, want %v", got.Found, tt.expectFound)
			}
			if got.Version != tt.expectVersion {
				t.Errorf("Version = %q, want %q", got.Version, tt.expectVersion)
			}
		})
	}
}

func TestParseGoModFileNotFound(t *testing.T) {
	_, err := ParseGoMod("/nonexistent/go.mod", "github.com/test/lib")
	if err == nil {
		t.Error("ParseGoMod expected error for non-existent file, got nil")
	}
}

func TestReadModulePath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(p, []byte("module gitea.example.com/BETS-V2/ts-utils\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadModulePath(p)
	if err != nil {
		t.Fatal(err)
	}
	if got != "gitea.example.com/BETS-V2/ts-utils" {
		t.Fatalf("got %q", got)
	}
}
