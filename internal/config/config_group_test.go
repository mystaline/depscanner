package config

import "testing"

func TestParseGitURL(t *testing.T) {
	host, owner, repo, err := ParseGitURL("https://gitea.example.com/BETS-V2/ts-utils.git")
	if err != nil {
		t.Fatal(err)
	}
	if host != "gitea.example.com" || owner != "BETS-V2" || repo != "ts-utils" {
		t.Fatalf("got %q/%q/%q", host, owner, repo)
	}
	if _, _, _, err := ParseGitURL("git@gitea.example.com:BETS-V2/ts-utils.git"); err != nil {
		t.Fatalf("scp-style url: %v", err)
	}
}

func TestProviderGroup(t *testing.T) {
	cases := []struct {
		p    Provider
		want string
	}{
		{Provider{Gitea: &GiteaProvider{Org: "BETS-V2"}}, "BETS-V2"},
		{Provider{Git: "https://h.com/acme/lib.git"}, "h.com-acme"},
		{Provider{Path: "/home/u/work/be-core-utils"}, "be-core-utils"},
		{Provider{Name: "custom", Path: "/home/u/work/be-core-utils"}, "custom"},
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
