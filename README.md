# depscanner

CLI tool that scans all repositories in a Gitea organization to detect shared Go library usage, version staleness per branch, sub-package import mapping, function-level call-site search, API diff between commits, and per-repo breaking change impact analysis.

Built for environments where multiple Go microservices depend on a single shared library and you need visibility into who uses what, how stale they are, and where specific functions are called.

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

## Configuration

Copy the example config and fill in your values:

```bash
cp configs/depscanner.example.yaml ~/.depscanner.yaml
```

```yaml
gitea:
  url: "https://gitea.example.com"
  token: "${GITEA_TOKEN}" # env vars are expanded
  org: "my-org"

target_module: "gitea.example.com/my-org/shared-lib"
cache_dir: "~/.depscanner/repos"

# Optional: only scan specific repos (supports glob patterns: *, ?, [...])
# include_repos:
#   - service-a
#   - "api-*"

# Optional: skip irrelevant repos (supports glob patterns)
exclude_repos:
  - docs
  - "*-temp"

# Branch tracking: maps repo branch to target module branch
# Used with --branch for staleness detection
branch_tracking:
  dev: dev
  staging: staging
  main: main
```

## What do I want to do?

### See which repos use the shared library

```bash
depscanner scan
```

Lists all org repos, marks which ones have a `go.mod`, and which depend on the target module.

### Check if repos are up-to-date on a specific branch

```bash
depscanner scan --branch dev
```

Compares each repo's `go.mod` pseudo-version against the latest commit on the target module's `dev` branch. Supports both standard (`v0.0.0-YYYYMMDD-hash`) and pre-release (`v1.1.0-dev.0.YYYYMMDD-hash`) pseudo-version formats. Shows `ok` or `STALE (have X, want Y)`.

Use this to answer: _"Which services on `dev` haven't updated the shared library yet?"_

### See which sub-packages are imported

```bash
depscanner scan --packages
```

Shows which top-level sub-packages each repo imports (e.g. `helper`, `service`).

Use this to answer: _"Which services use `helper`? Who imports `service`?"_

### Find all call sites of a specific function

```bash
depscanner scan --func "Must"
```

AST-based search across all repos. Finds every file and line where `Must` is called from any sub-package of the target module. Handles import aliases and dot imports.

Narrow to a specific package:

```bash
depscanner scan --func "helper.Must"
```

Use this to answer: _"Before I change `helper.Must`'s signature, which services call it and where?"_

### Combine flags

```bash
# Staleness + function search on staging
depscanner scan --branch staging --func "NewDBService"

# Sub-packages + staleness on dev, JSON output for scripting
depscanner scan --branch dev --packages --format json
```

### Use cached repos only (offline/fast mode)

```bash
depscanner scan --no-fetch --func "Must"
```

Skips `git fetch/clone` and uses whatever is already cached locally. Useful for repeated scans or offline work.

## All flags

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

### `diff` subcommand

Compare the target module's API between two commits, branches, or tags:

```bash
# Show all changes between two commits
depscanner diff --from abc123 --to def456

# Only show breaking changes
depscanner diff --from v1.0.0 --to main --breaking-only

# JSON output for CI
depscanner diff --from abc123 --to main --format json
```

Tracks all Go symbol types: functions, methods, structs, interfaces, constants, variables, and type aliases.

### `impact` subcommand

Combine diff results with call-site scanning to show per-repo upgrade checklists:

```bash
# Which repos break if we merge these changes?
depscanner impact --from abc123 --to main

# Scan consumer repos on a specific branch
depscanner impact --from abc123 --to main --branch dev
```

For each breaking change, shows exactly which repos call the affected symbol and at which file/line.

## Output formats

**Table** (default) -- colored terminal output with status icons:

- `✓` up to date
- `⚠` stale (behind latest)
- `✗` no go.mod / not used
- `?` pure tagged version with no commit hash (rare when using `go get lib@branch`)

**JSON** (`--format json`) -- machine-readable output for CI pipelines and scripting. Includes all scan data: repos, versions, staleness status, call sites, and summary counts.

## How it works

1. Fetches the repo list from the Gitea API
2. **Concurrently** syncs repos (4 workers) with real-time progress tracking
3. **Pipelines** analysis immediately as each repo finishes syncing (no waiting for all repos)
4. Parses `go.mod` files to find target module dependencies
5. If `--branch`: compares pseudo-version commit hashes against the target module's branch HEAD
6. If `--packages`: fast import-only AST parse to collect sub-package usage
7. If `--func`: two-pass AST scan — import-only first to filter files, then full parse to find `CallExpr` nodes with import alias resolution
8. `diff`: builds full symbol index at two refs using `go/ast` + `go/printer`, then compares all exported symbols
9. `impact`: cross-references diff results with call-site data across all consumer repos

Config supports glob patterns (`*`, `?`, `[...]`) in `include_repos` and `exclude_repos` for flexible repo filtering.

## Requirements

- Go 1.22+
- Git (for repo cloning/fetching)
- Gitea API token with read access to the target organization
- Works on Linux, macOS, and Windows (with a modern terminal for colored output)

## License

MIT
