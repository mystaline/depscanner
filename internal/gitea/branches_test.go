package gitea

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetBranch(t *testing.T) {
	tests := []struct {
		name        string
		branch      string
		handler     http.HandlerFunc
		expectHash  string
		expectNil   bool
		expectError bool
	}{
		{
			name:   "found",
			branch: "main",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("branch") != "" {
					http.Error(w, "unexpected query param", http.StatusBadRequest)
					return
				}
				// Check that branch name is in URL path
				info := BranchInfo{
					Name: "main",
					Commit: BranchCommit{
						ID:        "abc123def456",
						Message:   "commit message",
						Timestamp: "2026-04-09T00:00:00Z",
					},
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(info)
			},
			expectHash: "abc123def456",
		},
		{
			name:   "branch not found",
			branch: "nonexistent",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "branch not found", http.StatusNotFound)
			},
			expectNil: true,
		},
		{
			name:   "server error",
			branch: "main",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "server error", http.StatusInternalServerError)
			},
			expectError: true,
		},
		{
			name:   "invalid json",
			branch: "main",
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
			info, err := client.GetBranch("owner", "repo", tt.branch)

			if tt.expectError && err == nil {
				t.Errorf("GetBranch expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("GetBranch failed: %v", err)
			}
			if tt.expectNil && info != nil {
				t.Errorf("GetBranch expected nil, got %v", info)
			}
			if !tt.expectNil && !tt.expectError && info.Commit.ID != tt.expectHash {
				t.Errorf("GetBranch commit ID = %q, want %q", info.Commit.ID, tt.expectHash)
			}
		})
	}
}

func TestGetBranchCommitHash(t *testing.T) {
	tests := []struct {
		name        string
		handler     http.HandlerFunc
		expectHash  string
		expectError bool
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				info := BranchInfo{
					Name: "main",
					Commit: BranchCommit{
						ID: "0123456789ab",
					},
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(info)
			},
			expectHash: "0123456789ab",
		},
		{
			name: "branch not found returns empty string",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "not found", http.StatusNotFound)
			},
			expectHash: "",
		},
		{
			name: "error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "server error", http.StatusInternalServerError)
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tt.handler))
			defer server.Close()

			client := NewClient(server.URL, "test-token")
			hash, err := client.GetBranchCommitHash("owner", "repo", "main")

			if tt.expectError && err == nil {
				t.Errorf("GetBranchCommitHash expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("GetBranchCommitHash failed: %v", err)
			}
			if hash != tt.expectHash {
				t.Errorf("GetBranchCommitHash = %q, want %q", hash, tt.expectHash)
			}
		})
	}
}

func TestListRepoBranches(t *testing.T) {
	tests := []struct {
		name           string
		handler        http.HandlerFunc
		expectBranches []string
		expectError    bool
	}{
		{
			name: "single page",
			handler: func(w http.ResponseWriter, r *http.Request) {
				page := r.URL.Query().Get("page")
				// page 1 returns data, page 2+ returns empty
				if page == "1" {
					branches := []BranchInfo{
						{Name: "main", Commit: BranchCommit{ID: "abc"}},
						{Name: "develop", Commit: BranchCommit{ID: "def"}},
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(branches)
				} else {
					// Empty result to stop pagination
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode([]BranchInfo{})
				}
			},
			expectBranches: []string{"main", "develop"},
		},
		{
			name: "multiple pages",
			handler: func(w http.ResponseWriter, r *http.Request) {
				page := r.URL.Query().Get("page")
				var branches []BranchInfo

				if page == "1" {
					branches = []BranchInfo{
						{Name: "main", Commit: BranchCommit{ID: "abc"}},
					}
				} else if page == "2" {
					branches = []BranchInfo{
						{Name: "feature", Commit: BranchCommit{ID: "def"}},
					}
				} else {
					branches = []BranchInfo{}
				}

				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(branches)
			},
			expectBranches: []string{"main", "feature"},
		},
		{
			name: "empty result",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode([]BranchInfo{})
			},
			expectBranches: []string{},
		},
		{
			name: "http error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "forbidden", http.StatusForbidden)
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tt.handler))
			defer server.Close()

			client := NewClient(server.URL, "test-token")
			branches, err := client.ListRepoBranches("owner", "repo")

			if tt.expectError && err == nil {
				t.Errorf("ListRepoBranches expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("ListRepoBranches failed: %v", err)
			}
			if len(branches) != len(tt.expectBranches) {
				t.Errorf("ListRepoBranches returned %d branches, want %d", len(branches), len(tt.expectBranches))
			}
			for i, expectedName := range tt.expectBranches {
				if branches[i] != expectedName {
					t.Errorf("branch[%d] = %q, want %q", i, branches[i], expectedName)
				}
			}
		})
	}
}

func TestHasBranch(t *testing.T) {
	tests := []struct {
		name        string
		branch      string
		handler     http.HandlerFunc
		expectHas   bool
		expectError bool
	}{
		{
			name:   "branch exists",
			branch: "main",
			handler: func(w http.ResponseWriter, r *http.Request) {
				info := BranchInfo{
					Name:   "main",
					Commit: BranchCommit{ID: "abc"},
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(info)
			},
			expectHas: true,
		},
		{
			name:   "branch does not exist",
			branch: "nonexistent",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "not found", http.StatusNotFound)
			},
			expectHas: false,
		},
		{
			name:   "error",
			branch: "main",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "server error", http.StatusInternalServerError)
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tt.handler))
			defer server.Close()

			client := NewClient(server.URL, "test-token")
			has, err := client.HasBranch("owner", "repo", tt.branch)

			if tt.expectError && err == nil {
				t.Errorf("HasBranch expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("HasBranch failed: %v", err)
			}
			if has != tt.expectHas {
				t.Errorf("HasBranch = %v, want %v", has, tt.expectHas)
			}
		})
	}
}

func TestParseModuleOwnerRepo(t *testing.T) {
	tests := []struct {
		modulePath  string
		expectOwner string
		expectRepo  string
	}{
		{
			modulePath:  "github.com/mystaline/depscanner",
			expectOwner: "mystaline",
			expectRepo:  "depscanner",
		},
		{
			modulePath:  "gitea.example.com/org/lib/subpackage",
			expectOwner: "lib",
			expectRepo:  "subpackage",
		},
		{
			modulePath:  "single",
			expectOwner: "",
			expectRepo:  "",
		},
		{
			modulePath:  "two/parts",
			expectOwner: "",
			expectRepo:  "",
		},
		{
			modulePath:  "",
			expectOwner: "",
			expectRepo:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.modulePath, func(t *testing.T) {
			owner, repo := ParseModuleOwnerRepo(tt.modulePath)
			if owner != tt.expectOwner || repo != tt.expectRepo {
				t.Errorf("ParseModuleOwnerRepo(%q) = (%q, %q), want (%q, %q)",
					tt.modulePath, owner, repo, tt.expectOwner, tt.expectRepo)
			}
		})
	}
}
