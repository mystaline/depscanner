package config

import "testing"

func TestParseGitURL(t *testing.T) {
	host, owner, repo, err := ParseGitURL("https://gitea.example.com/org-a/acme-lib.git")
	if err != nil {
		t.Fatal(err)
	}
	if host != "gitea.example.com" || owner != "org-a" || repo != "acme-lib" {
		t.Fatalf("got %q/%q/%q", host, owner, repo)
	}
	if _, _, _, err := ParseGitURL("git@gitea.example.com:org-a/acme-lib.git"); err != nil {
		t.Fatalf("scp-style url: %v", err)
	}
}

func TestProviderGroup(t *testing.T) {
	cases := []struct {
		p    Provider
		want string
	}{
		{Provider{Gitea: &GiteaProvider{Org: "org-a"}}, "org-a"},
		{Provider{Git: "https://h.com/acme/lib.git"}, "h.com-acme"},
		{Provider{Path: "/home/u/work/acme-core"}, "acme-core"},
		{Provider{Name: "custom", Path: "/home/u/work/acme-core"}, "custom"},
	}
	for _, c := range cases {
		got, err := c.p.Group()
		if err != nil {
			t.Fatalf("%+v: %v", c.p, err)
		}
		if got != c.want {
			t.Fatalf("group(%+v) = %q, want %q", c.p, got, c.want)
		}
	}
}
