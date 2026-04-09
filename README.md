# depscanner

CLI tool that scans all repositories in an organization to detect shared Go library usage, version staleness per branch, sub-package import mapping, function-level call-site search, API diff between commits, and per-repo breaking change impact analysis.

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

```text
✓ RESOLVED my-cool-app (current: ...-2a019f321162)
  ✓ module/util.ProcessData — 2 call sites: (resolved in 97ce50f14a26)

⚠ ACTION REQUIRED data-processor (current: ...-568d8cd5539e)
  ~ module/util.ProcessData — 5 call sites: (needs commit 97ce50f14a26)
      internal/worker/job.go:112    util.ProcessData
```

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

## How it Works

1. **Discovery**: Fetches repository list via Gitea API.
2. **Syncing**: Concurrently clones/fetches repositories into a local cache.
3. **Pipelining**: Analyzes each repository immediately as it finishes syncing.
4. **Analysis**:
   - Parses `go.mod` for dependency versions.
   - Performs two-pass AST scanning for call-site detection.
   - Builds a full symbol index for structural and behavioral diffing.
   - **Surgical Resolution Tracking**: Uses `git log -L` and automated ancestry verification (with auto-unshallowing) to determine if a fix has been applied.

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
