package repo

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/mystaline/depscanner/internal/gitea"
)

// ProcessFunc is called for each repo after it has been synced.
// It receives the repo info and whether sync was successful.
type ProcessFunc func(repo gitea.Repository, synced bool)

// PipelineSyncAndProcess syncs repos concurrently (bounded by concurrency)
// and calls processFn for each repo as soon as its sync completes.
// Results are processed in non-deterministic order but the caller
// can buffer them for sorted output.
//
// concurrency controls how many git operations run in parallel.
// If concurrency <= 0, it defaults to 4.
func (m *Manager) PipelineSyncAndProcess(repos []gitea.Repository, noFetch bool, concurrency int, processFn ProcessFunc) {
	if concurrency <= 0 {
		concurrency = 4
	}

	if err := os.MkdirAll(m.cacheDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "  warn: create cache dir: %v\n", err)
		return
	}

	total := len(repos)
	var done int64
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)

	for _, r := range repos {
		wg.Add(1)
		sem <- struct{}{} // acquire semaphore slot

		go func(repo gitea.Repository) {
			defer wg.Done()
			defer func() { <-sem }() // release semaphore slot

			synced := true
			if !noFetch {
				if err := m.syncRepo(repo, false); err != nil {
					fmt.Fprintf(os.Stderr, "  warn: sync %s: %v\n", repo.Name, err)
					synced = false
				}
			}

			processFn(repo, synced)

			n := atomic.AddInt64(&done, 1)
			printProgress(repo.Name, synced, int(n), total)
		}(r)
	}

	wg.Wait()
	clearProgress()
}

// PipelineSyncBranchAndProcess is like PipelineSyncAndProcess but
// syncs a specific branch instead of the default branch.
func (m *Manager) PipelineSyncBranchAndProcess(repos []gitea.Repository, branch string, noFetch bool, concurrency int, processFn ProcessFunc) {
	if concurrency <= 0 {
		concurrency = 4
	}

	if err := os.MkdirAll(m.cacheDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "  warn: create cache dir: %v\n", err)
		return
	}

	total := len(repos)
	var done int64
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)

	for _, r := range repos {
		wg.Add(1)
		sem <- struct{}{} // acquire semaphore slot

		go func(repo gitea.Repository) {
			defer wg.Done()
			defer func() { <-sem }() // release semaphore slot

			synced := true
			if !noFetch {
				ok, err := m.syncBranchQuiet(repo.Name, repo.CloneURL, branch)
				if err != nil {
					fmt.Fprintf(os.Stderr, "  warn: sync %s@%s: %v\n", repo.Name, branch, err)
					synced = false
				} else {
					synced = ok
				}
			}

			processFn(repo, synced)

			n := atomic.AddInt64(&done, 1)
			printProgress(repo.Name, synced, int(n), total)
		}(r)
	}

	wg.Wait()
	clearProgress()
}

// printProgress displays an inline progress update using carriage return.
func printProgress(name string, synced bool, done, total int) {
	icon := "✓"
	if !synced {
		icon = "·"
	}
	// \r overwrites the current line. Pad with spaces to clear leftover chars.
	fmt.Fprintf(os.Stderr, "\r  [%d/%d] %s %s            ", done, total, name, icon)
}

// clearProgress moves to a new line after the progress indicator is done.
func clearProgress() {
	fmt.Fprintf(os.Stderr, "\r                                                    \r")
}

// syncBranchQuiet is a non-printing version of SyncBranch for use
// in concurrent pipelines where interleaved output would be messy.
func (m *Manager) syncBranchQuiet(repoName, cloneURL, branch string) (bool, error) {
	if !isValidBranchName(branch) {
		return false, fmt.Errorf("invalid branch name: %s", branch)
	}

	dest := m.GetRepoPath(repoName)

	// Clone if not yet cached.
	if _, err := os.Stat(filepath.Join(dest, ".git")); os.IsNotExist(err) {
		cmd := exec.Command("git", "clone", "--depth=1", "--quiet", "--branch", branch, "--", cloneURL, dest)
		if out, err := cmd.CombinedOutput(); err != nil {
			if isBranchNotFound(out) {
				return false, nil
			}
			return false, fmt.Errorf("git clone %s@%s: %s", repoName, branch, firstLine(out))
		}
		return true, nil
	}

	// Fetch specific branch.
	fetch := exec.Command("git", "-C", dest, "fetch", "--depth=1", "--quiet", "origin", "--", branch)
	if out, err := fetch.CombinedOutput(); err != nil {
		if isBranchNotFound(out) {
			return false, nil
		}
		return false, fmt.Errorf("git fetch %s@%s: %s", repoName, branch, firstLine(out))
	}

	// Checkout FETCH_HEAD.
	checkout := exec.Command("git", "-C", dest, "checkout", "--quiet", "-B", branch, "FETCH_HEAD")
	if out, err := checkout.CombinedOutput(); err != nil {
		return false, fmt.Errorf("checkout %s: %s", branch, firstLine(out))
	}

	return true, nil
}
