# depscanner

![Depscanner Banner](assets/banner.png)

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat-square&logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/License-MIT-green?style=flat-square)](LICENSE)
[![Stability](https://img.shields.io/badge/Stability-Stable-success?style=flat-square)](#)
[![Test Coverage](https://img.shields.io/badge/coverage-analysis%2047%25%20%7C%20config%2086%25%20%7C%20gitea%2090%25-blue?style=flat-square)](#testing)

A high-performance CLI tool designed for large-scale Go organizations to manage shared library dependencies. depscanner analyzes impact, tracks architectural debt, and validates API compatibility across hundreds of repositories.

## Key Features

- **Deep AST Analysis**: True function-level call-site tracking using Go's Abstract Syntax Tree. Understands package aliases and method calls (`obj.Method()`).
- **High-Performance Pipeline**: Concurrently syncs and processes multiple repositories using a worker-pool architecture.
- **Surgical Resolution Tracking**: Identifies if a fix has been applied by tracing symbol history through git log -L and ancestry checks.
- **Impact Analysis**: Automatically cross-references API breaking changes with actual call sites in consumer applications.
- **Transparent Reporting**: Displays how functions are actually called in code (e.g., `util.ProcessData`) for intuitive debugging.
- **Gitea Native**: Full integration with Gitea organization APIs.
- **Behavioral Audit**: Detects logic changes even when function signatures remain identical using SHA-256 body hashing.

## Install

```bash
# From source
go install github.com/mystaline/depscanner/cmd/depscanner@latest

# Or clone and build
git clone https://github.com/mystaline/depscanner.git
cd depscanner
make build        # outputs bin/depscanner
make install      # installs to $GOPATH/bin
```

Requires Go 1.22+ and git.

### Windows Setup

depscanner runs natively on Windows and WSL2. Ensure the following are installed:

1. **Go 1.22+**: Download from [golang.org](https://golang.org/dl)
2. **Git**: Install [Git for Windows](https://git-scm.com/download/win) or use `choco install git` if using Chocolatey
3. **Verify Installation**:
   ```cmd
   go version
   git --version
   ```

**Cache Directory**: On Windows, depscanner stores cached repositories in `%USERPROFILE%\.depscanner\repos` by default. You can override with `--cache-dir` flag or set `cache_dir` in config.

**Terminal Support**: Output works best in Windows Terminal, PowerShell 7+, or modern cmd.exe. Older cmd.exe may not display Unicode symbols correctly.

**WSL2**: If using WSL2 Fedora/Ubuntu, install Go and Git in the WSL distribution, then use depscanner normally. Cache directories are inside WSL.

## Configuration

Copy the example config and fill in your values:

```bash
cp configs/depscanner.example.yaml ~/.depscanner.yaml
```

```yaml
gitea:
  url: "https://gitea.com"
  token: "${GITEA_TOKEN}" # env vars are expanded
  org: "my-community"

target_module: "github.com/example/awesome-lib"
cache_dir: "~/.depscanner/repos"

# Optional: only scan specific repos (supports glob patterns: *, ?, [...])
# include_repos:
#   - gopher-app
#   - "service-*"

# Optional: skip irrelevant repos (supports glob patterns)
exclude_repos:
  - junk
  - "test-*"

# Branch tracking: maps repo branch to target module branch
# Used with --branch for staleness detection
branch_tracking:
  dev: main
  main: main
```

## Usage

### 1. List repos using the shared library

```bash
depscanner scan
```

Lists all repositories in the organization and detects dependency status.

**Example Output:**

```text
  STATUS  REPOSITORY       TARGET VERSION
  ------  ----------       --------------
  ✓       my-cool-app      v1.2.0-20260409024228-2a019f321162
  ✗       docs-site        (no go.mod)
  ·       legacy-app       (not used)
```

- `✓`: Uses the target module.
- `✗`: Does not have a `go.mod` file.
- `·`: Has a `go.mod` file but does not import the target module.

### 2. Check for staleness on a specific branch

```bash
depscanner scan --branch main
```

**Example Output:**

```text
  STATUS  REPOSITORY       VERSION/COMMIT  STALENESS
  ------  ----------       --------------  ---------
  ✓       gopher-api       2a019f321162    up to date
  ⚠       data-processor   568d8cd5539e    STALE (have 568d8cd, want 2a019f)
```

Use this to answer: _"Which services on `dev` haven't updated the shared library yet?"_

### 3. Map sub-package imports

```bash
depscanner scan --packages
```

**Example Output:**

```text
  my-cool-app        util, config, internal/core
  gopher-api         util, database
```

### 4. Find call sites of a specific function

```bash
depscanner scan --func "util.ProcessData"
```

**Example Output:**

```text
  my-cool-app (2 call sites):
    internal/app/main.go:42            util.ProcessData
    internal/app/handler.go:18         util.ProcessData
```

### 5. Signature mismatch validation

```bash
depscanner scan --func "NewClient" --check
```

### 6. Detect API changes (diff)

Compare the target module's API between two refs:

```bash
depscanner diff --from 1.0.0 --to 1.1.0
```

**Example Output:**

```text

  ✗  REMOVED           core.OldFunction               [BREAKING]
  ~  LOGIC_CHANGED     util.ProcessData               [LOGIC]
  +  ADDED             core.NewFunction               [additive]
```

- `✗ REMOVED/SIGNATURE_CHANGED`: Compatibility breaking changes.
- `~ LOGIC_CHANGED`: Internal logic change without changing the signature (behavioral change).
- `+ ADDED`: New symbol added (safe/additive).

### 7. Analyze upgrade impact

Generate a per-repo upgrade checklist based on API changes:

```bash
depscanner impact --from abc123 --to main
```

**Example Output:**

````text
✓ RESOLVED my-cool-app (current: ...-2a019f321162)
  ✓ module/util.ProcessData — 2 call sites: (resolved in 97ce50f14a26)

⚠ ACTION REQUIRED data-processor (current: ...-568d8cd5539e)
  ~ module/util.ProcessData — 5 call sites: (needs commit 97ce50f14a26)
      internal/worker/job.go:112    util.ProcessData

### 8. Diagnostic Mode

If no impacts are found, Depscanner provides a transparent breakdown of what was scanned:

```text
Checked 3 impactful symbols across all reservoirs:
  ~ legacy.API.DeprecatedMethod: 0 call sites
  ~ legacy.NewClient: 0 call sites
  ✓ No consumers are actually affected by these changes.
````

- **RESOLVED**: Repository is using a version that already contains the fix (verified via git ancestry).
- **ACTION REQUIRED**: Repository is still using an old version and is affected by the changes. Shows which commit contains the fix.

## Command Flags

| Flag          | Description                                                            |
| ------------- | ---------------------------------------------------------------------- |
| `--config`    | Config file path (default: `~/.depscanner.yaml`)                       |
| `--cache-dir` | Override local repo cache directory                                    |
| `--format`    | Output format: `table` (default) or `json`                             |
| `--no-fetch`  | Skip git fetch, use cached repos only                                  |
| `--branch`    | Scan a specific branch and enable staleness detection                  |
| `--packages`  | Show which sub-packages of the target module are imported              |
| `--func`      | Search for call sites of a function (e.g. `"Must"` or `"helper.Must"`) |
| `--check`     | Validate call-site arg counts against target signature (with `--func`) |

## Output Formats

- **Table** (default): Human-readable terminal output with colored status icons.
- **JSON** (`--format json`): Machine-readable output for CI/CD pipelines and scripting.

## Example Workflow

**Scenario:** Planning to update `github.com/example/shared-lib` from v1.0.0 to v1.2.0 across multiple repos.

```bash
# 1. Detect which repos use shared-lib
depscanner scan

# 2. Compare API between versions
depscanner diff --from v1.0.0 --to v1.2.0

# 3. See which repos are affected and need fixes
depscanner impact --from v1.0.0 --to v1.2.0

# Output shows:
# ✓ RESOLVED api-app (already on v1.2.0 — safe)
# ⚠ ACTION REQUIRED worker-app (on v1.0.0 — has 3 call sites needing updates)
# ⚠ ACTION REQUIRED batch-job (on v1.0.0 — has 1 call site needing updates)
```

This takes the guesswork out of: "Can we update this lib? Which repos need attention? What exactly will break?"

## How it Works

1. **Discovery**: Fetches repository list via Gitea API.
2. **Syncing**: Concurrently clones/fetches repositories into a local cache.
3. **Pipelining**: Analyzes each repository immediately as it finishes syncing.
4. **Analysis**:
   - Parses `go.mod` for dependency versions.
   - Performs two-pass AST scanning for call-site detection.
   - Builds a full symbol index for structural and behavioral diffing.
   - **Surgical Resolution Tracking**: Uses `git log -L` and automated ancestry verification (with auto-unshallowing) to determine if a fix has been applied.

## Testing

Run the full test suite:

```bash
go test ./...
```

Run tests with verbose output:

```bash
go test ./... -v
```

Run tests with coverage report:

```bash
go test ./... -cover
```

**Current coverage:**

- `internal/analysis`: 48.7% (diff, impact, version, gomod parsing)
- `internal/config`: 86.0% (config loading, validation, env expansion)
- `internal/gitea`: 90.7% (Gitea API client mocking)

Tests include:

- Version and pseudo-version parsing (semver comparison, staleness detection)
- Go.mod parsing (single-line and block requires, comments, pseudo-versions)
- Configuration loading (env var expansion, validation, branch tracking)
- Gitea API client (pagination, error handling, authentication)
- Symbol diffing (breaking changes, logic changes, interface modifications)
- Impact analysis (call site matching, repo sorting, summary generation)

## Requirements

- Go 1.22+
- Git
- Gitea API token with read access

## Roadmap

- [ ] **Multi-Language Support**: Extend analysis beyond Go (e.g., TypeScript/npm, Python/pip).
- [ ] **Platform Agnostic**: Native integration with GitHub, GitLab, and Bitbucket APIs.
- [ ] **Dependency Graph Visualization**: Interactive web-based UI to explore the impact graph.
- [ ] **Automated PR Suggestions**: Propose Pull Requests for consumer repositories with suggested fixes for simple breaking changes.
- [ ] **IDE Integration**: VS Code extension to show impact analysis directly in the editor.

## License

MIT
