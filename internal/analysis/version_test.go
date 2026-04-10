package analysis

import (
	"testing"
	"time"
)

func TestIsPseudoVersion(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		// Valid pseudo-versions
		{
			input: "v0.0.0-20260311025516-abcdef123456",
			want:  true,
		},
		{
			input: "v1.1.0-dev.0.20260409024228-2a019f321162",
			want:  true,
		},
		{
			input: "v1.2.3-20260101000000-0123456789ab",
			want:  true,
		},
		// Invalid pseudo-versions
		{
			input: "v1.2.3",
			want:  false,
		},
		{
			input: "v1.2.3-alpha",
			want:  false,
		},
		{
			input: "v0.0.0-20260311025516-invalid",
			want:  false,
		},
		{
			input: "v0.0.0-20260311025516-abc",
			want:  false,
		},
		{
			input: "v0.0.0-abc-abcdef123456",
			want:  false,
		},
		{
			input: "",
			want:  false,
		},
		{
			input: "not-a-version",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := IsPseudoVersion(tt.input)
			if got != tt.want {
				t.Errorf("IsPseudoVersion(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParsePseudoVersion(t *testing.T) {
	tests := []struct {
		input      string
		wantBase   string
		wantHash   string
		wantYear   int
		wantMonth  int
		wantDay    int
		shouldFail bool
	}{
		{
			input:     "v0.0.0-20260311025516-abcdef123456",
			wantBase:  "v0.0.0",
			wantHash:  "abcdef123456",
			wantYear:  2026,
			wantMonth: 3,
			wantDay:   11,
		},
		{
			input:     "v1.1.0-dev.0.20260409024228-2a019f321162",
			wantBase:  "v1.1.0",
			wantHash:  "2a019f321162",
			wantYear:  2026,
			wantMonth: 4,
			wantDay:   9,
		},
		{
			input:      "v1.2.3",
			shouldFail: true,
		},
		{
			input:      "v0.0.0-20260311025516-invalid",
			shouldFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParsePseudoVersion(tt.input)
			if tt.shouldFail {
				if err == nil {
					t.Errorf("ParsePseudoVersion(%q) expected error, got nil", tt.input)
				}
				return
			}

			if err != nil {
				t.Errorf("ParsePseudoVersion(%q) failed: %v", tt.input, err)
				return
			}

			if got.BaseVersion != tt.wantBase {
				t.Errorf("BaseVersion = %q, want %q", got.BaseVersion, tt.wantBase)
			}
			if got.CommitHash != tt.wantHash {
				t.Errorf("CommitHash = %q, want %q", got.CommitHash, tt.wantHash)
			}
			if got.Timestamp.Year() != tt.wantYear || int(got.Timestamp.Month()) != tt.wantMonth || got.Timestamp.Day() != tt.wantDay {
				t.Errorf("Timestamp = %v, want %04d-%02d-%02d", got.Timestamp, tt.wantYear, tt.wantMonth, tt.wantDay)
			}
		})
	}
}

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a    string
		b    string
		want int
		desc string
	}{
		// Equal versions
		{"v1.2.3", "v1.2.3", 0, "exact match"},
		{"1.2.3", "v1.2.3", 0, "with/without v prefix"},
		{"v1.2.3-alpha", "v1.2.3-beta", 0, "ignores pre-release"},

		// a < b
		{"v1.0.0", "v1.1.0", -1, "patch differs"},
		{"v1.0.0", "v2.0.0", -1, "major differs"},
		{"v0.1.0", "v1.0.0", -1, "major differs (0.x)"},

		// a > b
		{"v2.0.0", "v1.9.9", 1, "major version higher"},
		{"v1.2.0", "v1.1.9", 1, "minor version higher"},
		{"v1.2.3", "v1.2.0", 1, "patch version higher"},

		// Edge cases
		{"v1.2.3+incompatible", "v1.2.3", 0, "ignores metadata"},
		{"v1", "v1.0.0", 0, "incomplete semver"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got := CompareSemver(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("CompareSemver(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestCommitsBehind(t *testing.T) {
	now := time.Now()

	tests := []struct {
		current      time.Time
		latest       time.Time
		expectString string
		desc         string
	}{
		{
			current:      now.Add(-30 * time.Minute),
			latest:       now,
			expectString: "just behind",
			desc:         "30 minutes ago",
		},
		{
			current:      now.Add(-12 * time.Hour),
			latest:       now,
			expectString: "~12 hours behind",
			desc:         "12 hours ago",
		},
		{
			current:      now.Add(-7 * 24 * time.Hour),
			latest:       now,
			expectString: "~7 days behind",
			desc:         "7 days ago",
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got := CommitsBehind(tt.current, tt.latest)
			if got != tt.expectString {
				t.Errorf("CommitsBehind() = %q, want %q", got, tt.expectString)
			}
		})
	}
}
