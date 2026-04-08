# depscanner

CLI tool that scans all repositories in a Gitea organization to detect shared Go library usage, version staleness per branch, sub-package import mapping, and function-level call-site search.

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

# Optional: only scan specific repos
# include_repos:
#   - service-a
#   - service-b

# Optional: skip irrelevant repos
exclude_repos:
  - docs

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

## Output formats

**Table** (default) -- colored terminal output with status icons:

- `✓` up to date
- `⚠` stale (behind latest)
- `✗` no go.mod / not used
- `?` pure tagged version with no commit hash (rare when using `go get lib@branch`)

**JSON** (`--format json`) -- machine-readable output for CI pipelines and scripting. Includes all scan data: repos, versions, staleness status, call sites, and summary counts.

## How it works

1. Fetches the repo list from the Gitea API
2. Shallow-clones (or fetches) each repo into a local cache
3. Parses `go.mod` files to find target module dependencies
4. If `--branch`: compares pseudo-version commit hashes against the target module's branch HEAD
5. If `--packages`: fast import-only AST parse to collect sub-package usage
6. If `--func`: two-pass AST scan -- import-only first to filter files, then full parse to find `CallExpr` nodes with import alias resolution

## Requirements

- Go 1.22+
- Git (for repo cloning/fetching)
- Gitea API token with read access to the target organization
- Works on Linux, macOS, and Windows (with a modern terminal for colored output)

## License

MIT
