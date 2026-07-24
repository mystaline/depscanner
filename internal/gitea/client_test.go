package gitea

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListOrgRepos(t *testing.T) {
	tests := []struct {
		name            string
		handler         http.HandlerFunc
		expectRepos     int
		expectError     bool
		expectRepoNames []string
	}{
		{
			name: "single page",
			handler: func(w http.ResponseWriter, r *http.Request) {
				page := r.URL.Query().Get("page")
				// page 1 returns data, page 2+ returns empty
				if page == "1" {
					repos := []Repository{
						{Name: "repo1", CloneURL: "https://example.com/org/repo1.git", DefaultBranch: "main"},
						{Name: "repo2", CloneURL: "https://example.com/org/repo2.git", DefaultBranch: "main"},
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(repos)
				} else {
					// Empty result to stop pagination
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode([]Repository{})
				}
			},
			expectRepos:     2,
			expectRepoNames: []string{"repo1", "repo2"},
		},
		{
			name: "multiple pages",
			handler: func(w http.ResponseWriter, r *http.Request) {
				page := r.URL.Query().Get("page")
				var repos []Repository

				if page == "1" {
					repos = []Repository{
						{Name: "repo1", CloneURL: "https://example.com/org/repo1.git"},
						{Name: "repo2", CloneURL: "https://example.com/org/repo2.git"},
					}
				} else if page == "2" {
					repos = []Repository{
						{Name: "repo3", CloneURL: "https://example.com/org/repo3.git"},
					}
				} else {
					repos = []Repository{}
				}

				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(repos)
			},
			expectRepos:     3,
			expectRepoNames: []string{"repo1", "repo2", "repo3"},
		},
		{
			name: "empty result",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode([]Repository{})
			},
			expectRepos: 0,
		},
		{
			name: "http error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
			},
			expectError: true,
		},
		{
			name: "invalid json",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte("invalid json"))
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tt.handler))
			defer server.Close()

			client := NewClient(server.URL, "test-token")
			repos, err := client.ListOrgRepos("test-org")

			if tt.expectError && err == nil {
				t.Errorf("ListOrgRepos expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("ListOrgRepos failed: %v", err)
			}
			if len(repos) != tt.expectRepos {
				t.Errorf("ListOrgRepos returned %d repos, want %d", len(repos), tt.expectRepos)
			}
			for i, expectedName := range tt.expectRepoNames {
				if repos[i].Name != expectedName {
					t.Errorf("repo[%d].Name = %q, want %q", i, repos[i].Name, expectedName)
				}
			}
		})
	}
}

func TestGetFileContent(t *testing.T) {
	tests := []struct {
		name        string
		handler     http.HandlerFunc
		expectBody  string
		expectNil   bool
		expectError bool
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("ref") != "main" {
					http.Error(w, "wrong ref", http.StatusBadRequest)
					return
				}
				w.Write([]byte("module content"))
			},
			expectBody: "module content",
		},
		{
			name: "not found returns nil",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "not found", http.StatusNotFound)
			},
			expectNil: true,
		},
		{
			name: "server error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "server error", http.StatusInternalServerError)
			},
			expectError: true,
		},
		{
			name: "unauthorized",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tt.handler))
			defer server.Close()

			client := NewClient(server.URL, "test-token")
			body, err := client.GetFileContent("owner", "repo", "go.mod", "main")

			if tt.expectError && err == nil {
				t.Errorf("GetFileContent expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("GetFileContent failed: %v", err)
			}
			if tt.expectNil && body != nil {
				t.Errorf("GetFileContent expected nil body, got %v", string(body))
			}
			if !tt.expectNil && !tt.expectError && string(body) != tt.expectBody {
				t.Errorf("GetFileContent body = %q, want %q", string(body), tt.expectBody)
			}
		})
	}
}

func TestNewClient(t *testing.T) {
	tests := []struct {
		baseURL       string
		expectBaseURL string
	}{
		{
			baseURL:       "https://gitea.example.com",
			expectBaseURL: "https://gitea.example.com",
		},
		{
			baseURL:       "https://gitea.example.com/",
			expectBaseURL: "https://gitea.example.com",
		},
		{
			baseURL:       "https://gitea.example.com///",
			expectBaseURL: "https://gitea.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.baseURL, func(t *testing.T) {
			client := NewClient(tt.baseURL, "token")
			if client.baseURL != tt.expectBaseURL {
				t.Errorf("baseURL = %q, want %q", client.baseURL, tt.expectBaseURL)
			}
			if client.token != "token" {
				t.Errorf("token = %q, want \"token\"", client.token)
			}
		})
	}
}

func TestClientAuthHeader(t *testing.T) {
	// Verify Authorization header is sent correctly
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader != "token my-secret-token" {
			http.Error(w, fmt.Sprintf("wrong auth header: %s", authHeader), http.StatusUnauthorized)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "my-secret-token")
	body, err := client.GetFileContent("owner", "repo", "file.txt", "main")

	if err != nil {
		t.Errorf("GetFileContent failed: %v", err)
	}
	if string(body) != "ok" {
		t.Errorf("unexpected response: %s", body)
	}
}
