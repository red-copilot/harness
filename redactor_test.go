package harness

import (
	"encoding/json"
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

func TestRedactJSONPreservesStructure(t *testing.T) {
	r := NewRedactor("supersecret")
	got := r.RedactJSON([]byte(`{"outer":{"token":"supersecret"},"flag":"flag{abc}"}`))
	if strings.Contains(string(got), "supersecret") || strings.Contains(string(got), "abc") {
		t.Fatalf("secret remained: %s", got)
	}
	var payload map[string]any
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, got)
	}
}

func TestContainsDetectsKnownSecret(t *testing.T) {
	r := NewRedactor("supersecret")
	if !r.Contains("prefix supersecret suffix") {
		t.Fatal("known secret not detected")
	}
	if !r.Contains("token=abc") {
		t.Fatal("secret-like pattern not detected")
	}
}
