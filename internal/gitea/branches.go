package gitea

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// BranchInfo holds the latest commit details for a branch.
type BranchInfo struct {
	Name   string       `json:"name"`
	Commit BranchCommit `json:"commit"`
}

// BranchCommit holds the commit metadata returned by the Gitea branch API.
type BranchCommit struct {
	ID        string `json:"id"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"` // ISO 8601
}

// GetBranch fetches branch info including the latest commit hash.
// Returns nil, nil if the branch does not exist (HTTP 404).
func (c *Client) GetBranch(owner, repo, branch string) (*BranchInfo, error) {
	u := fmt.Sprintf("%s/api/v1/repos/%s/%s/branches/%s",
		c.baseURL, url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(branch))

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get branch %s: %w", branch, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get branch %s: HTTP %d", branch, resp.StatusCode)
	}

	var info BranchInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decode branch: %w", err)
	}
	return &info, nil
}

// GetBranchCommitHash is a convenience method that returns just the latest commit hash
// for a branch on the target module's repo. Returns empty string if branch not found.
func (c *Client) GetBranchCommitHash(owner, repo, branch string) (string, error) {
	info, err := c.GetBranch(owner, repo, branch)
	if err != nil {
		return "", err
	}
	if info == nil {
		return "", nil
	}
	return info.Commit.ID, nil
}

// ListRepoBranches returns all branch names for a repo.
func (c *Client) ListRepoBranches(owner, repo string) ([]string, error) {
	var all []string
	for page := 1; page <= 100; page++ {
		names, err := c.listBranchesPage(owner, repo, page)
		if err != nil {
			return nil, err
		}
		if len(names) == 0 {
			break
		}
		all = append(all, names...)
	}
	return all, nil
}

func (c *Client) listBranchesPage(owner, repo string, page int) ([]string, error) {
	u := fmt.Sprintf("%s/api/v1/repos/%s/%s/branches?limit=50&page=%d",
		c.baseURL, url.PathEscape(owner), url.PathEscape(repo), page)

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list branches page %d: %w", page, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list branches: HTTP %d", resp.StatusCode)
	}

	var branches []BranchInfo
	if err := json.NewDecoder(resp.Body).Decode(&branches); err != nil {
		return nil, fmt.Errorf("decode branches: %w", err)
	}

	names := make([]string, len(branches))
	for i, b := range branches {
		names[i] = b.Name
	}
	return names, nil
}

// HasBranch checks whether a specific branch exists in a repo by checking
// if the branch name is present. Uses a HEAD-like approach via GetBranch.
func (c *Client) HasBranch(owner, repo, branch string) (bool, error) {
	info, err := c.GetBranch(owner, repo, branch)
	if err != nil {
		return false, err
	}
	return info != nil, nil
}

// ParseModuleOwnerRepo extracts the owner and repo name from a Go module path.
func ParseModuleOwnerRepo(modulePath string) (owner, repo string) {
	parts := strings.Split(modulePath, "/")
	if len(parts) < 3 {
		return "", ""
	}
	return parts[len(parts)-2], parts[len(parts)-1]
}
