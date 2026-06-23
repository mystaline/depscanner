// Package repo manages a local cache of cloned repositories.
package repo

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mystaline/depscanner/internal/gitea"
)

var validBranchRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]*$`)

// gitClone runs git clone with --filter=blob:none, falling back to a plain
// clone if the server does not support partial clone (exit 128).
// dest must be the last element of args.
func gitClone(args ...string) error {
	dest := args[len(args)-1]
	filtered := append([]string{"clone", "--filter=blob:none"}, args...)
	if err := exec.Command("git", filtered...).Run(); err == nil {
		return nil
	}
	// Remove any partial clone left by the failed attempt before retrying.
	_ = os.RemoveAll(dest)
	plain := append([]string{"clone"}, args...)
	return exec.Command("git", plain...).Run()
}

// clearStaleLocks removes lock files left by previously interrupted git operations.
func clearStaleLocks(repoPath string) {
	for _, lock := range []string{"shallow.lock", "index.lock"} {
		_ = os.Remove(filepath.Join(repoPath, ".git", lock))
	}
}

// isShallow reports whether the repo at repoPath is a shallow clone.
func isShallow(repoPath string) bool {
	_, err := os.Stat(filepath.Join(repoPath, ".git", "shallow"))
	return err == nil
}

// isValidBranchName checks that a branch name is safe to pass to git commands.
func isValidBranchName(name string) bool {
	return validBranchRe.MatchString(name)
}

// Manager clones and updates repos into a local cache directory.
type Manager struct {
	cacheDir string
}

// NewManager creates a Manager that stores repos under cacheDir/org.
func NewManager(cacheDir, org string) *Manager {
	return &Manager{cacheDir: filepath.Join(cacheDir, org)}
}

// SyncRepos clones any repo not yet cached and pulls updates for existing ones.
// When noFetch is true, already-cloned repos are left as-is.
func (m *Manager) SyncRepos(repos []gitea.Repository, noFetch bool) error {
	if err := os.MkdirAll(m.cacheDir, 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	for _, r := range repos {
		if err := m.syncRepo(r, noFetch); err != nil {
			return fmt.Errorf("sync %s: %w", r.Name, err)
		}
	}
	return nil
}

func (m *Manager) syncRepo(r gitea.Repository, noFetch bool) error {
	dest := m.GetRepoPath(r.Name)
	clearStaleLocks(dest)

	// Clone if the repo doesn't exist locally yet.
	if _, err := os.Stat(filepath.Join(dest, ".git")); os.IsNotExist(err) {
		if err := gitClone("--depth=1", "--quiet", r.CloneURL, dest); err != nil {
			return fmt.Errorf("git clone: %w", err)
		}
		return nil
	}

	if noFetch {
		return nil
	}

	// Fetch and reset the currently checked-out branch, not the default branch.
	// The cache dir is shared with local development — a hard reset to
	// origin/main while another branch is checked out causes divergence.
	currentBranch := getCurrentBranch(dest)
	if currentBranch != "HEAD" {
		// Preserve unshallowed repos: if the repo was deepened (e.g. by
		// impact/diff/unshallow), fetch without --depth to avoid re-shallowing
		// which can be extremely slow on large repos.
		fetchArgs := []string{"-C", dest, "fetch", "--quiet", "origin", currentBranch}
		if isShallow(dest) {
			fetchArgs = []string{"-C", dest, "fetch", "--depth=1", "--quiet", "origin", currentBranch}
		}
		fetch := exec.Command("git", fetchArgs...)
		fetch.Stdout = nil
		fetch.Stderr = nil
		_ = fetch.Run() // best-effort

		reset := exec.Command("git", "-C", dest, "reset", "--hard", "--quiet", "origin/"+currentBranch)
		reset.Stdout = nil
		reset.Stderr = nil
		_ = reset.Run() // best-effort
	}

	return nil
}

// SyncBranch fetches and checks out a specific branch for a repo.
// If the branch doesn't exist remotely, returns false with no error.
func (m *Manager) SyncBranch(repoName, cloneURL, branch string) (bool, error) {
	if !isValidBranchName(branch) {
		return false, fmt.Errorf("invalid branch name: %s", branch)
	}

	dest := m.GetRepoPath(repoName)
	clearStaleLocks(dest)

	// Clone if the repo doesn't exist locally yet.
	if _, err := os.Stat(filepath.Join(dest, ".git")); os.IsNotExist(err) {
		if err := gitClone("--depth=1", "--quiet", "--branch", branch, "--", cloneURL, dest); err != nil {
			return false, fmt.Errorf("git clone %s@%s failed", repoName, branch)
		}
		return true, nil
	}

	// Fetch the specific branch.
	// Preserve unshallowed repos — re-shallowing is slow and can hang.
	fetchArgs := []string{"-C", dest, "fetch", "--quiet", "origin", "--", branch}
	if isShallow(dest) {
		fetchArgs = []string{"-C", dest, "fetch", "--depth=1", "--quiet", "origin", "--", branch}
	}
	fetch := exec.Command("git", fetchArgs...)
	fetch.Stdout = nil
	fetch.Stderr = nil
	if err := fetch.Run(); err != nil {
		return false, nil
	}

	// Checkout using FETCH_HEAD and set upstream tracking so future pulls don't diverge.
	checkout := exec.Command("git", "-C", dest, "checkout", "--quiet", "-B", branch, "FETCH_HEAD")
	checkout.Stdout = nil
	checkout.Stderr = nil
	if err := checkout.Run(); err != nil {
		return false, fmt.Errorf("checkout %s failed", branch)
	}

	_ = exec.Command("git", "-C", dest, "branch", "--set-upstream-to=origin/"+branch, branch).Run()

	return true, nil
}

// GetOrgPath returns the local path for the organization directory.
func (m *Manager) GetOrgPath() string {
	return m.cacheDir
}

// ListLocalRepos lists all directories in the org cache as repositories.
func (m *Manager) ListLocalRepos() ([]gitea.Repository, error) {
	orgPath := m.GetOrgPath()
	entries, err := os.ReadDir(orgPath)
	if err != nil {
		return nil, err
	}

	var repos []gitea.Repository
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			repos = append(repos, gitea.Repository{
				Name: entry.Name(),
			})
		}
	}
	return repos, nil
}

// GetRepoPath returns the local path for a given repo name.
func (m *Manager) GetRepoPath(name string) string {
	return filepath.Join(m.cacheDir, name)
}

// CheckoutCommit checks out a specific commit or ref in the cached repo.
// It fetches the ref first (in case the shallow clone doesn't have it),
// then does a detached HEAD checkout.
func (m *Manager) CheckoutCommit(repoName, ref string) error {
	dest := m.GetRepoPath(repoName)
	if _, err := os.Stat(filepath.Join(dest, ".git")); os.IsNotExist(err) {
		return fmt.Errorf("repo %s not cloned yet", repoName)
	}
	clearStaleLocks(dest)

	// Attempt to fetch the ref.
	fetch := exec.Command("git", "-C", dest, "fetch", "--quiet", "origin", ref)
	fetch.Stdout = nil
	fetch.Stderr = nil
	_ = fetch.Run() // best-effort

	// Try checkout — works for branches, tags, and commit hashes.
	checkout := exec.Command("git", "-C", dest, "checkout", "--quiet", "--detach", ref)
	checkout.Stdout = nil
	checkout.Stderr = nil
	if err := checkout.Run(); err != nil {
		// If ref is a branch name, try FETCH_HEAD instead.
		checkout2 := exec.Command("git", "-C", dest, "checkout", "--quiet", "--detach", "FETCH_HEAD")
		checkout2.Stdout = nil
		checkout2.Stderr = nil
		if err2 := checkout2.Run(); err2 != nil {
			return fmt.Errorf("checkout %s in %s failed: %w", ref, repoName, err)
		}
	}

	return nil
}

// getCurrentBranch returns the currently checked-out branch, or "HEAD" if detached.
func getCurrentBranch(dest string) string {
	cmd := exec.Command("git", "-C", dest, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "HEAD"
	}
	return strings.TrimSpace(string(out))
}

// isBranchNotFound checks if git output indicates a missing remote ref.
func isBranchNotFound(out []byte) bool {
	s := strings.ToLower(string(out))
	return strings.Contains(s, "couldn't find remote ref") ||
		strings.Contains(s, "remote branch") && strings.Contains(s, "not found") ||
		strings.Contains(s, "remote ref does not exist")
}

// firstLine returns the first non-empty line from git output for clean error messages.
func firstLine(out []byte) string {
	s := strings.TrimSpace(string(out))
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}
