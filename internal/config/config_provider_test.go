package config

import "testing"

func TestProviderLocation(t *testing.T) {
	cases := []struct {
		name    string
		p       Provider
		want    string
		wantErr bool
	}{
		{"gitea", Provider{Gitea: &GiteaProvider{Org: "o"}}, "gitea", false},
		{"git", Provider{Git: "https://h/o/r.git"}, "git", false},
		{"path", Provider{Path: "/tmp/x"}, "path", false},
		{"none", Provider{}, "", true},
		{"two", Provider{Git: "u", Path: "p"}, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.p.Location()
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
