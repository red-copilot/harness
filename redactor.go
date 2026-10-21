package harness

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
)

const RedactedValue = "[REDACTED]"

var flagLikePattern = regexp.MustCompile(`(?i)(flag|ctf|tsec)\{[^}]{1,256}\}`)

// Redactor removes known secret values and obvious secret-like flags.
type Redactor struct{ values []string }

func NewRedactor(values ...string) *Redactor {
	seen := map[string]struct{}{}
	clean := make([]string, 0, len(values))
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

func (r *Redactor) RedactJSON(value []byte) []byte { return redactJSONPreserve(r, value) }

func (r *Redactor) Contains(value string) bool {
	if r != nil {
		for _, secret := range r.values {
			if strings.Contains(value, secret) {
				return true
			}
		}
	}
	return flagLikePattern.MatchString(value) || containsSecretLikeValue(value)
}

func redactJSONPreserve(r *Redactor, value []byte) []byte {
	if len(value) == 0 {
		return value
	}
	var decoded any
	if err := json.Unmarshal(value, &decoded); err == nil {
		cleaned := redactJSONValue(r, decoded)
		if encoded, err := json.Marshal(cleaned); err == nil {
			return encoded
		}
	}
	if r == nil {
		return value
	}
	return []byte(r.Redact(string(value)))
}

func redactJSONValue(r *Redactor, value any) any {
	switch typed := value.(type) {
	case string:
		if r == nil {
			return value
		}
		return r.Redact(typed)
	case []any:
		for i := range typed {
			typed[i] = redactJSONValue(r, typed[i])
		}
		return typed
	case map[string]any:
		for key, item := range typed {
			typed[key] = redactJSONValue(r, item)
		}
		return typed
	default:
		return value
	}
}

func redactorForSpec(spec RunSpec) *Redactor {
	names := append([]string(nil), spec.SecretEnv...)
	values := []string{}
	agents := append([]AgentSpec{spec.Planner}, spec.Executors...)
	for _, agent := range agents {
		for _, value := range agent.Env {
			values = append(values, value)
		}
		names = append(names, agent.EnvFrom...)
		for key, value := range agent.Env {
			for _, secretName := range spec.SecretEnv {
				if key == secretName {
					values = append(values, value)
				}
			}
		}
	}
	for _, name := range names {
		if value, ok := os.LookupEnv(name); ok {
			values = append(values, value)
		}
	}
	return NewRedactor(values...)
}

func containsSecretLikeValue(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{"password=", "token=", "flag{", "ctf{", "tsec{", "benchmark_token", "authorization: bearer"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
