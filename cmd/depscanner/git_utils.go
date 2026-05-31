package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// clearStaleLocks removes lock files left by previously interrupted git operations.
func clearStaleLocks(repoPath string) {
	for _, lock := range []string{"shallow.lock", "index.lock"} {
		_ = os.Remove(filepath.Join(repoPath, ".git", lock))
	}
}

const (
	fetchMaxRetries  = 5
	fetchRetryDelay  = 3 * time.Second
	// 500 MB — prevents early EOF on large packs (git default is 1 MB)
	httpPostBuffer   = 524288000
)

// unshallowTargetRepo deepens history for the given branches only.
// Non-existent branches on the remote are silently skipped.
func unshallowTargetRepo(repoPath string, timeout time.Duration, branches []string) error {
	clearStaleLocks(repoPath)

	// Fix single-branch refspec from shallow clone so all branches are fetchable.
	_ = exec.Command("git", "-C", repoPath, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*").Run()
	// Large postBuffer prevents early EOF when fetching large packs over HTTP.
	_ = exec.Command("git", "-C", repoPath, "config", "http.postBuffer", fmt.Sprintf("%d", httpPostBuffer)).Run()

	for _, branch := range branches {
		for attempt := 1; attempt <= fetchMaxRetries; attempt++ {
			clearStaleLocks(repoPath)
			if attempt == 1 {
				fmt.Printf("\n  [%s] ", branch)
			} else {
				fmt.Printf("\n  [%s] retry %d/%d ", branch, attempt, fetchMaxRetries)
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			args := []string{"-C", repoPath, "fetch", "--filter=blob:none", "--depth=2147483647", "--progress", "origin", branch}
			cmd := exec.CommandContext(ctx, "git", args...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			err := cmd.Run()
			if err != nil {
				// Fallback: server may not support partial clone — retry without filter.
				clearStaleLocks(repoPath)
				args = []string{"-C", repoPath, "fetch", "--depth=2147483647", "--progress", "origin", branch}
				cmd = exec.CommandContext(ctx, "git", args...)
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				err = cmd.Run()
			}
			cancel()
			if err == nil {
				break
			}
			if attempt < fetchMaxRetries {
				fmt.Printf("\n  fetch failed (%v), retrying in %s...", err, fetchRetryDelay)
				time.Sleep(fetchRetryDelay)
			}
		}
	}
	fmt.Println()
	return nil
}

// isAncestor checks if the ancestor commit hash is in the history of the descendant.
// It will attempt to fetch missing commits from remote if necessary.
func isAncestor(repoPath, ancestor, descendant string) bool {
	if ancestor == "" || descendant == "" {
		return false
	}
	if strings.HasPrefix(descendant, ancestor) || strings.HasPrefix(ancestor, descendant) {
		return true
	}

	// Double check if descendant exists locally, if not, try to fetch it
	if err := exec.Command("git", "-C", repoPath, "rev-parse", "--verify", descendant).Run(); err != nil {
		// Attempt to fetch from origin (might help find the hash if it's on a known branch)
		_ = exec.Command("git", "-C", repoPath, "fetch", "origin", descendant, "--quiet").Run()
	}

	cmd := exec.Command("git", "-C", repoPath, "merge-base", "--is-ancestor", ancestor, descendant)
	return cmd.Run() == nil
}

// shortenHash returns the first 12 characters of a git hash.
func shortenHash(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

// firstLine returns the first non-empty line from git output for clean error messages.
func firstLine(out []byte) string {
	s := strings.TrimSpace(string(out))
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}
