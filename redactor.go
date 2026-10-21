package harness

import (
	"os"
	"regexp"
	"sort"
	"strings"
)

const RedactedValue = "[REDACTED]"

var flagLikePattern = regexp.MustCompile(`(?i)(flag|ctf|tsec)\{[^}]{1,256}\}`)

// Redactor removes known secret values from persisted text. It is intentionally
// best-effort: callers should never put credentials in prompts or logs.
type Redactor struct {
	values []string
}

func NewRedactor(values ...string) *Redactor {
	seen := map[string]struct{}{}
	var clean []string
	for _, value := range values {
		if len(value) < 4 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		clean = append(clean, value)
	}
	sort.Slice(clean, func(i, j int) bool { return len(clean[i]) > len(clean[j]) })
	return &Redactor{values: clean}
}

func (r *Redactor) Redact(value string) string {
	if r != nil {
		for _, secret := range r.values {
			value = strings.ReplaceAll(value, secret, RedactedValue)
		}
	}
	return flagLikePattern.ReplaceAllString(value, RedactedValue)
}

func (r *Redactor) RedactJSON(value []byte) []byte {
	if len(value) == 0 || r == nil {
		return value
	}
	return []byte(r.Redact(string(value)))
}

func redactorForSpec(spec RunSpec) *Redactor {
	names := append([]string(nil), spec.SecretEnv...)
	valuesFromEnv := []string{}
	for _, agent := range spec.Agents {
		for _, value := range agent.Env {
			valuesFromEnv = append(valuesFromEnv, value)
		}
		names = append(names, agent.EnvFrom...)
		if agent.Pi != nil {
			names = append(names, agent.Pi.EnvPassthrough...)
		}
		for key := range agent.Env {
			for _, secretName := range spec.SecretEnv {
				if key == secretName {
					names = append(names, key)
				}
			}
		}
	}
	values := append([]string(nil), valuesFromEnv...)
	for _, name := range names {
		if value, ok := os.LookupEnv(name); ok {
			values = append(values, value)
		}
	}
	return NewRedactor(values...)
}
