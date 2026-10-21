package harness

import (
	"strings"
	"testing"
)

func TestRedactorRedactsFlagLikeValues(t *testing.T) {
	r := NewRedactor()
	got := r.Redact("submit flag{abc123} and CTF{secret} now")
	if strings.Contains(got, "abc123") || strings.Contains(got, "secret") {
		t.Fatalf("flag-like values were not redacted: %q", got)
	}
}
