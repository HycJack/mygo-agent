package cli

import "testing"

// TestRedact proves a credential never survives into a card or a log,
// while the key stays visible so the ask is still readable
// (spec/approvals.md: the approval line is bounded and redacted).
func TestRedact(t *testing.T) {
	cases := []struct {
		name string
		in   string
		// mustNot appear anywhere in the output
		absent []string
		// must appear, so the line is still recognisable
		present []string
	}{
		{
			name:    "authorization header",
			in:      `curl -H "Authorization: Bearer sk-abcdefgh12345678" https://api.example.com`,
			absent:  []string{"sk-abcdefgh12345678"},
			present: []string{"Authorization", "api.example.com", "redacted"},
		},
		{
			name:    "authorization without scheme",
			in:      `curl -H 'authorization: hunter2xyz' https://x.test`,
			absent:  []string{"hunter2xyz"},
			present: []string{"authorization", "x.test"},
		},
		{
			name:    "flag form",
			in:      `mytool --api-key sk-abcdefgh12345678 --verbose`,
			absent:  []string{"sk-abcdefgh12345678"},
			present: []string{"--api-key", "--verbose"},
		},
		{
			name:    "equals form",
			in:      `export GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123`,
			absent:  []string{"ghp_abcdefghijklmnopqrstuvwxyz0123"},
			present: []string{"GITHUB_TOKEN"},
		},
		{
			name:    "quoted value keeps the quote",
			in:      `AWS_SECRET_ACCESS_KEY="AKIAIOSFODNN7EXAMPLE"`,
			absent:  []string{"AKIAIOSFODNN7EXAMPLE"},
			present: []string{"AWS_SECRET_ACCESS_KEY", `"`, "redacted"},
		},
		{
			name:    "bare token prefix anywhere",
			in:      `echo xoxb-123456789012-abcdefghijkl && go build ./...`,
			absent:  []string{"xoxb-123456789012-abcdefghijkl"},
			present: []string{"go build"},
		},
		{
			name:    "ordinary long text is untouched",
			in:      "go test ./internal/... && ./scripts/check-deps.sh && echo done",
			present: []string{"go test", "check-deps.sh", "done"},
		},
		{
			name:    "a long path is not a secret",
			in:      "cat /Users/someone/Library/Caches/go-build/0123456789abcdef/cache",
			present: []string{"/Users/someone/Library/Caches"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.in)
			for _, s := range tc.absent {
				if contains(got, s) {
					t.Fatalf("Redact(%q) = %q, still contains the secret %q", tc.in, got, s)
				}
			}
			for _, s := range tc.present {
				if !contains(got, s) {
					t.Fatalf("Redact(%q) = %q, lost %q; the line must stay readable", tc.in, got, s)
				}
			}
		})
	}
}

// TestRedactIsIdempotent guards the redaction pass against itself: the
// host redacts on the way into a card and the display helpers may redact
// again, and a second pass must not eat the marker or the key.
func TestRedactIsIdempotent(t *testing.T) {
	once := Redact(`curl -H "Authorization: Bearer sk-abcdefgh12345678"`)
	twice := Redact(once)
	if once != twice {
		t.Fatalf("Redact is not idempotent:\n once: %q\ntwice: %q", once, twice)
	}
}

// TestRedactThenTrunc guards the ordering the caller depends on: a
// credential must be masked before the line is cut, or the secret hides
// past the ellipsis.
func TestRedactThenTrunc(t *testing.T) {
	long := `curl -H "Authorization: Bearer sk-abcdefgh12345678" https://` +
		"very-long-hostname-that-pushes-the-line-past-the-cut.example.com/" +
		"and/more/path/segments/to/make/it/long"
	got := Trunc(Redact(long), 60)
	if contains(got, "sk-abcdefgh12345678") {
		t.Fatalf("the secret survived a truncated summary: %q", got)
	}
	if !contains(got, "Authorization") {
		t.Fatalf("the key should still be visible: %q", got)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		}()
}
