# Multi-Org Support for depscanner

**Date:** 2026-05-29
**Status:** Approved

## Problem

depscanner currently scans repos from a single Gitea org. Users with repos spread across multiple orgs (e.g. `BETS-Global-Settings`, `BETS-Fixed-Asset`) must run separate invocations with different configs.

## Design

### Config Schema

Add `gitea.orgs` as an optional multi-org field. All existing config fields (`gitea.org`, top-level `include_repos`, `exclude_repos`) remain unchanged — single-org configs continue to work without modification.

```yaml
gitea:
  url: "https://gitea.qwertysystem.net"
  token: "${GITEA_TOKEN}"
  orgs:
    - name: BETS-Global-Settings
      include_repos: [ts-global-settings]   # optional
    - name: BETS-Fixed-Asset
      include_repos: [fats, fats-api-template]
      exclude_repos: [fats-old]             # optional
```

**Resolution rule:**
- If `gitea.orgs` is set → use multi-org mode (iterate orgs list)
- If only `gitea.org` is set → existing single-org behavior, unchanged

### New Struct

```go
// OrgConfig holds per-org settings for multi-org mode.
type OrgConfig struct {
    Name         string   `yaml:"name"`
    IncludeRepos []string `yaml:"include_repos"`
    ExcludeRepos []string `yaml:"exclude_repos"`
}
```

`GiteaConfig` gains `Orgs []OrgConfig`. `Validate()` accepts either `org` or `orgs` being set.

### Pipeline Changes (cmd_scan only)

`runScan` resolves the org list once:
- Multi-org: iterate `cfg.Gitea.Orgs`, per org: `ListOrgRepos(org.Name)` → `filterRepos(repos, org.IncludeRepos, org.ExcludeRepos)` → existing pipeline
- Single-org: existing path unchanged

Output groups results by org with a header per org when in multi-org mode.

### Unchanged

- `cmd_diff`, `cmd_impact` — operate on `target_module` only, no org iteration needed
- `repo.Manager` — already scoped per org via `NewManager(cacheDir, org)`, called once per org
- `filterRepos` — reused as-is
- Cache structure — `<cacheDir>/<org>/<repo>` already correct for multi-org

## Files to Change

| File | Change |
|------|--------|
| `internal/config/config.go` | Add `OrgConfig` struct, `Orgs []OrgConfig` to `GiteaConfig`, update `Validate()` |
| `cmd/depscanner/cmd_scan.go` | Resolve org list, iterate per org, group output |
| `configs/depscanner.example.yaml` | Add commented multi-org example |

## Backward Compatibility

Existing `.depscanner.yaml` files with `gitea.org` work without any changes.
