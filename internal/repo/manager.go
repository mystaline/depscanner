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

	// Clone if the repo doesn't exist locally yet.
	if _, err := os.Stat(filepath.Join(dest, ".git")); os.IsNotExist(err) {
		cmd := exec.Command("git", "clone", "--depth=1", "--quiet", r.CloneURL, dest)
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git clone: %w", err)
		}
		return nil
	}

	if noFetch {
		return nil
	}

	branch := r.DefaultBranch
	if branch == "" {
		branch = "main"
	}

	fetch := exec.Command("git", "-C", dest, "fetch", "--depth=1", "--quiet", "origin", branch)
	fetch.Stdout = nil
	fetch.Stderr = nil
	if err := fetch.Run(); err != nil {
		return fmt.Errorf("git fetch: %w", err)
	}

	reset := exec.Command("git", "-C", dest, "reset", "--hard", "--quiet", "origin/"+branch)
	reset.Stdout = nil
	reset.Stderr = nil
	if err := reset.Run(); err != nil {
		return fmt.Errorf("git reset: %w", err)
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

	// Clone if the repo doesn't exist locally yet.
	if _, err := os.Stat(filepath.Join(dest, ".git")); os.IsNotExist(err) {
		cmd := exec.Command("git", "clone", "--depth=1", "--quiet", "--branch", branch, "--", cloneURL, dest)
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Run(); err != nil {
			return false, fmt.Errorf("git clone %s@%s failed", repoName, branch)
		}
		return true, nil
	}

	// Fetch the specific branch.
	fetch := exec.Command("git", "-C", dest, "fetch", "--depth=1", "--quiet", "origin", "--", branch)
	fetch.Stdout = nil
	fetch.Stderr = nil
	if err := fetch.Run(); err != nil {
		return false, nil
	}

	// Checkout using FETCH_HEAD
	checkout := exec.Command("git", "-C", dest, "checkout", "--quiet", "-B", branch, "FETCH_HEAD")
	checkout.Stdout = nil
	checkout.Stderr = nil
	if err := checkout.Run(); err != nil {
		return false, fmt.Errorf("checkout %s failed", branch)
	}

	return true, nil
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
		_ = checkout2.Run()
	}

	return nil
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
