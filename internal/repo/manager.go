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
	// Use bare-ish clone without --single-branch so all branches are fetchable.
	if _, err := os.Stat(filepath.Join(dest, ".git")); os.IsNotExist(err) {
		fmt.Printf("  cloning  %s\n", r.Name)
		cmd := exec.Command("git", "clone", "--depth=1", r.CloneURL, dest)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
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

	fmt.Printf("  fetching %s\n", r.Name)
	fetch := exec.Command("git", "-C", dest, "fetch", "--depth=1", "origin", branch)
	fetch.Stdout = os.Stdout
	fetch.Stderr = os.Stderr
	if err := fetch.Run(); err != nil {
		return fmt.Errorf("git fetch: %w", err)
	}

	reset := exec.Command("git", "-C", dest, "reset", "--hard", "origin/"+branch)
	reset.Stdout = os.Stdout
	reset.Stderr = os.Stderr
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
		fmt.Printf("  cloning  %s@%s...", repoName, branch)
		cmd := exec.Command("git", "clone", "--depth=1", "--quiet", "--branch", branch, "--", cloneURL, dest)
		if out, err := cmd.CombinedOutput(); err != nil {
			if isBranchNotFound(out) {
				fmt.Printf(" skip (no %s branch)\n", branch)
				return false, nil
			}
			fmt.Printf(" FAIL\n")
			return false, fmt.Errorf("git clone %s@%s: %s", repoName, branch, firstLine(out))
		}
		fmt.Printf(" ok\n")
		return true, nil
	}

	// Fetch the specific branch. With --depth=1, this updates FETCH_HEAD
	// but does not create a remote tracking ref (origin/<branch>).
	fmt.Printf("  syncing  %s@%s...", repoName, branch)
	fetch := exec.Command("git", "-C", dest, "fetch", "--depth=1", "--quiet", "origin", "--", branch)
	if out, err := fetch.CombinedOutput(); err != nil {
		if isBranchNotFound(out) {
			fmt.Printf(" skip (no %s branch)\n", branch)
			return false, nil
		}
		fmt.Printf(" FAIL\n")
		return false, fmt.Errorf("git fetch %s@%s: %s", repoName, branch, firstLine(out))
	}

	// Checkout using FETCH_HEAD (not origin/<branch>) since shallow fetch
	// doesn't create remote tracking refs.
	checkout := exec.Command("git", "-C", dest, "checkout", "--quiet", "-B", branch, "FETCH_HEAD")
	if out, err := checkout.CombinedOutput(); err != nil {
		fmt.Printf(" FAIL\n")
		return false, fmt.Errorf("checkout %s: %s", branch, firstLine(out))
	}

	fmt.Printf(" ok\n")
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

	// Attempt to fetch the ref (branch name or commit hash).
	// For branch names this updates FETCH_HEAD; for commits it may fail
	// if the shallow clone doesn't have it, which is fine — we'll try checkout anyway.
	fetch := exec.Command("git", "-C", dest, "fetch", "--quiet", "origin", ref)
	_ = fetch.Run() // best-effort

	// Try checkout — works for branches, tags, and commit hashes.
	checkout := exec.Command("git", "-C", dest, "checkout", "--quiet", "--detach", ref)
	if out, err := checkout.CombinedOutput(); err != nil {
		// If ref is a branch name, try FETCH_HEAD instead.
		checkout2 := exec.Command("git", "-C", dest, "checkout", "--quiet", "--detach", "FETCH_HEAD")
		if out2, err2 := checkout2.CombinedOutput(); err2 != nil {
			return fmt.Errorf("checkout %s: %s / %s", ref, firstLine(out), firstLine(out2))
		}
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
