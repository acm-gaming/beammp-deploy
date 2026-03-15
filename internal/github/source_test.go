package github

import "testing"

func TestParseRepository_Valid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in    string
		owner string
		repo  string
	}{
		{in: "https://github.com/org/repo", owner: "org", repo: "repo"},
		{in: "http://github.com/org/repo", owner: "org", repo: "repo"},
		{in: "https://github.com/org/repo.git", owner: "org", repo: "repo"},
		{in: "github.com/org/repo", owner: "org", repo: "repo"},
		{in: "GitHub.com/org/repo", owner: "org", repo: "repo"},
		{in: "org/repo", owner: "org", repo: "repo"},
		{in: "git@github.com:org/repo.git", owner: "org", repo: "repo"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			owner, repo, err := parseRepository(tc.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if owner != tc.owner || repo != tc.repo {
				t.Fatalf("unexpected parse result: got %s/%s want %s/%s", owner, repo, tc.owner, tc.repo)
			}
		})
	}
}

func TestParseRepository_Invalid(t *testing.T) {
	t.Parallel()

	tests := []string{
		"",
		" ",
		"https://gitlab.com/org/repo",
		"http://example.com/org/repo",
		"https://github.com/org",
		"https://github.com/org/repo/extra",
		"github.com/org",
		"org",
		"org/repo/extra",
		"https://github.com/org/repo?x=1",
		"https://github.com/org/repo#frag",
		"ssh://github.com/org/repo",
	}

	for _, in := range tests {
		in := in
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			_, _, err := parseRepository(in)
			if err == nil {
				t.Fatalf("expected error for %q", in)
			}
		})
	}
}
