package core

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

func TestRedactJSONRedactsObjectKeysAndHandlesCollisions(t *testing.T) {
	r := NewRedactor("supersecret")
	got := r.RedactJSON([]byte(`{"supersecret":"value","[REDACTED]":"existing"}`))
	if strings.Contains(string(got), "supersecret") {
		t.Fatalf("secret remained in JSON key: %s", got)
	}
	var payload map[string]any
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, got)
	}
	if len(payload) != 2 {
		t.Fatalf("collision lost a key: %s", got)
	}
	if payload[RedactedValue] != "existing" || payload[RedactedValue+"#2"] != "value" {
		t.Fatalf("unexpected collision result: %s", got)
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

func TestRedactorHandlesEveryRegisteredSecretLength(t *testing.T) {
	r := NewRedactor("abc")
	if got := r.Redact("prefix abc suffix"); strings.Contains(got, "abc") {
		t.Fatalf("short registered secret was not redacted: %q", got)
	}
	if !r.Contains("prefix abc suffix") {
		t.Fatal("short registered secret was not detected")
	}
}
