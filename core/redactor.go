package core

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Redactor removes known secret values from persisted and model-visible data.
const RedactedValue = "[REDACTED]"

var flagLikePattern = regexp.MustCompile(`(?i)(flag|ctf|tsec)\{[^}]{1,256}\}`)

type Redactor struct{ values []string }

func NewRedactor(values ...string) *Redactor {
	seen := map[string]struct{}{}
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	sort.Slice(unique, func(i, j int) bool { return len(unique[i]) > len(unique[j]) })
	return &Redactor{values: unique}
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
	if err := json.Unmarshal(value, &decoded); err != nil {
		return []byte(r.Redact(string(value)))
	}
	encoded, err := json.Marshal(redactJSONValue(r, decoded))
	if err != nil {
		return []byte(r.Redact(string(value)))
	}
	return encoded
}

func redactJSONValue(r *Redactor, value any) any {
	switch typed := value.(type) {
	case string:
		return r.Redact(typed)
	case []any:
		for i := range typed {
			typed[i] = redactJSONValue(r, typed[i])
		}
		return typed
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		redacted := make(map[string]any, len(typed))
		for _, original := range keys {
			key := r.Redact(original)
			candidate := key
			for suffix := 2; ; suffix++ {
				if _, exists := redacted[candidate]; !exists {
					break
				}
				candidate = fmt.Sprintf("%s#%d", key, suffix)
			}
			redacted[candidate] = redactJSONValue(r, typed[original])
		}
		return redacted
	default:
		return value
	}
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

func RedactObservation(r *Redactor, observation Observation) Observation {
	observation.ID = r.Redact(observation.ID)
	observation.RunID = r.Redact(observation.RunID)
	observation.IntentID = r.Redact(observation.IntentID)
	observation.AttemptID = r.Redact(observation.AttemptID)
	observation.Kind = r.Redact(observation.Kind)
	observation.Summary = r.Redact(observation.Summary)
	observation.Target = r.Redact(observation.Target)
	for i := range observation.Evidence {
		observation.Evidence[i] = RedactArtifact(r, observation.Evidence[i])
	}
	return observation
}

func RedactArtifact(r *Redactor, artifact Artifact) Artifact {
	artifact.RunID = r.Redact(artifact.RunID)
	artifact.AttemptID = r.Redact(artifact.AttemptID)
	artifact.SessionID = r.Redact(artifact.SessionID)
	artifact.Name = r.Redact(artifact.Name)
	artifact.MediaType = r.Redact(artifact.MediaType)
	return artifact
}

func RedactResult(r *Redactor, result Result) Result {
	result.Error = r.Redact(result.Error)
	if result.Verdict != nil {
		result.Verdict.Message = r.Redact(result.Verdict.Message)
		result.Verdict.Evidence = redactJSONPreserve(r, result.Verdict.Evidence)
	}
	for i := range result.Observations {
		result.Observations[i] = RedactObservation(r, result.Observations[i])
	}
	for i := range result.Artifacts {
		result.Artifacts[i] = RedactArtifact(r, result.Artifacts[i])
	}
	for i := range result.Facts {
		result.Facts[i] = RedactFact(r, result.Facts[i])
	}
	for i := range result.Intents {
		result.Intents[i] = RedactIntent(r, result.Intents[i])
	}
	for i := range result.Hints {
		result.Hints[i] = RedactHint(r, result.Hints[i])
	}
	for i := range result.IntentAttempts {
		result.IntentAttempts[i] = RedactIntentAttempt(r, result.IntentAttempts[i])
	}
	for i := range result.Sessions {
		result.Sessions[i].Error = r.Redact(result.Sessions[i].Error)
	}
	return result
}

func RedactFact(r *Redactor, fact Fact) Fact {
	fact.ID = r.Redact(fact.ID)
	fact.RunID = r.Redact(fact.RunID)
	fact.TaskID = r.Redact(fact.TaskID)
	fact.Description = r.Redact(fact.Description)
	fact.SourceIntentID = r.Redact(fact.SourceIntentID)
	for i := range fact.ObservationIDs {
		fact.ObservationIDs[i] = r.Redact(fact.ObservationIDs[i])
	}
	for i := range fact.ArtifactIDs {
		fact.ArtifactIDs[i] = r.Redact(fact.ArtifactIDs[i])
	}
	return fact
}

func RedactIntent(r *Redactor, intent Intent) Intent {
	intent.ID = r.Redact(intent.ID)
	intent.RunID = r.Redact(intent.RunID)
	intent.Description = r.Redact(intent.Description)
	intent.ExecutorID = r.Redact(intent.ExecutorID)
	intent.ResultFactID = r.Redact(intent.ResultFactID)
	intent.Error = r.Redact(intent.Error)
	for i := range intent.FromFactIDs {
		intent.FromFactIDs[i] = r.Redact(intent.FromFactIDs[i])
	}
	for i := range intent.AttemptIDs {
		intent.AttemptIDs[i] = r.Redact(intent.AttemptIDs[i])
	}
	return intent
}

func RedactHint(r *Redactor, hint Hint) Hint {
	hint.ID = r.Redact(hint.ID)
	hint.RunID = r.Redact(hint.RunID)
	hint.Content = r.Redact(hint.Content)
	hint.Creator = r.Redact(hint.Creator)
	return hint
}

func RedactIntentAttempt(r *Redactor, attempt IntentAttempt) IntentAttempt {
	attempt.ID = r.Redact(attempt.ID)
	attempt.RunID = r.Redact(attempt.RunID)
	attempt.IntentID = r.Redact(attempt.IntentID)
	attempt.SessionID = r.Redact(attempt.SessionID)
	attempt.Conclusion = r.Redact(attempt.Conclusion)
	attempt.Error = r.Redact(attempt.Error)
	return attempt
}
