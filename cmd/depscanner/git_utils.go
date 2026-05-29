package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// unshallowTargetRepo ensures the repository has full history and is configured
// to fetch all branches (not just the default one from a shallow clone).
func unshallowTargetRepo(repoPath string) {
	// Fix single-branch refspec from shallow clone so all branches are fetchable.
	_ = exec.Command("git", "-C", repoPath, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*").Run()

	if _, err := os.Stat(filepath.Join(repoPath, ".git", "shallow")); err == nil {
		_ = exec.Command("git", "-C", repoPath, "fetch", "--unshallow", "--quiet").Run()
	}

	// Fetch all remote branches now that the refspec covers everything.
	_ = exec.Command("git", "-C", repoPath, "fetch", "--all", "--quiet").Run()
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
