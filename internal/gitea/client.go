// Package gitea provides a minimal Gitea API client for listing repositories
// and reading file content.
package gitea

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Repository is the subset of Gitea's repository object that depscanner needs.
type Repository struct {
	Name          string `json:"name"`
	CloneURL      string `json:"clone_url"`
	DefaultBranch string `json:"default_branch"`
	Language      string `json:"language"`
	Empty         bool   `json:"empty"`
}

// Client is a minimal Gitea REST API client.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient creates a Client for the given Gitea instance URL and API token.
func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// ListOrgRepos returns all repositories belonging to org, walking all pages.
func (c *Client) ListOrgRepos(org string) ([]Repository, error) {
	var all []Repository
	for page := 1; page <= 100; page++ {
		batch, err := c.listOrgReposPage(org, page)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		all = append(all, batch...)
	}
	return all, nil
}

func (c *Client) listOrgReposPage(org string, page int) ([]Repository, error) {
	u := fmt.Sprintf("%s/api/v1/orgs/%s/repos?limit=50&page=%d",
		c.baseURL, url.PathEscape(org), page)

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list repos page %d: %w", page, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list repos: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var repos []Repository
	if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
		return nil, fmt.Errorf("decode repos: %w", err)
	}
	return repos, nil
}

// GetFileContent fetches the raw content of filePath at the given ref (branch/tag/commit).
// Returns nil, nil when the file does not exist (HTTP 404).
func (c *Client) GetFileContent(owner, repo, filePath, ref string) ([]byte, error) {
	rawURL := fmt.Sprintf("%s/api/v1/repos/%s/%s/raw/%s",
		c.baseURL, url.PathEscape(owner), url.PathEscape(repo), filePath)

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}
	q := u.Query()
	q.Set("ref", ref)
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "token "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get file %s: %w", filePath, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get file %s: HTTP %d", filePath, resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}
