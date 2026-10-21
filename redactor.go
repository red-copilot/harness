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

func redactBranch(r *Redactor, branch Branch) Branch {
	branch.ID = r.Redact(branch.ID)
	branch.ParentID = r.Redact(branch.ParentID)
	branch.RunID = r.Redact(branch.RunID)
	branch.TaskID = r.Redact(branch.TaskID)
	branch.Topic = r.Redact(branch.Topic)
	branch.Goal = r.Redact(branch.Goal)
	branch.Hypothesis = r.Redact(branch.Hypothesis)
	branch.SuccessCondition = r.Redact(branch.SuccessCondition)
	branch.FailureCondition = r.Redact(branch.FailureCondition)
	branch.Scope = r.Redact(branch.Scope)
	for i := range branch.ReviewOf {
		branch.ReviewOf[i] = r.Redact(branch.ReviewOf[i])
	}
	return branch
}
func redactAttempt(r *Redactor, attempt Attempt) Attempt {
	attempt.ID = r.Redact(attempt.ID)
	attempt.RunID = r.Redact(attempt.RunID)
	attempt.BranchID = r.Redact(attempt.BranchID)
	attempt.SessionID = r.Redact(attempt.SessionID)
	attempt.Error = r.Redact(attempt.Error)
	for i := range attempt.ObservationIDs {
		attempt.ObservationIDs[i] = r.Redact(attempt.ObservationIDs[i])
	}
	return attempt
}
func redactObservation(r *Redactor, observation Observation) Observation {
	observation.ID = r.Redact(observation.ID)
	observation.RunID = r.Redact(observation.RunID)
	observation.BranchID = r.Redact(observation.BranchID)
	observation.AttemptID = r.Redact(observation.AttemptID)
	observation.Kind = r.Redact(observation.Kind)
	observation.Summary = r.Redact(observation.Summary)
	observation.Target = r.Redact(observation.Target)
	for i := range observation.Evidence {
		observation.Evidence[i] = redactArtifact(r, observation.Evidence[i])
	}
	return observation
}
func redactArtifact(r *Redactor, artifact Artifact) Artifact {
	artifact.RunID = r.Redact(artifact.RunID)
	artifact.AttemptID = r.Redact(artifact.AttemptID)
	artifact.SessionID = r.Redact(artifact.SessionID)
	artifact.Name = r.Redact(artifact.Name)
	artifact.MediaType = r.Redact(artifact.MediaType)
	return artifact
}
func redactKnowledge(r *Redactor, knowledge Knowledge) Knowledge {
	knowledge.TaskID = r.Redact(knowledge.TaskID)
	knowledge.RunID = r.Redact(knowledge.RunID)
	knowledge.Topic = r.Redact(knowledge.Topic)
	knowledge.Statement = r.Redact(knowledge.Statement)
	knowledge.Target = r.Redact(knowledge.Target)
	for i := range knowledge.Sources {
		knowledge.Sources[i].RunID = r.Redact(knowledge.Sources[i].RunID)
		knowledge.Sources[i].AttemptID = r.Redact(knowledge.Sources[i].AttemptID)
		knowledge.Sources[i].SessionID = r.Redact(knowledge.Sources[i].SessionID)
		knowledge.Sources[i].Observation = r.Redact(knowledge.Sources[i].Observation)
	}
	return knowledge
}
func redactResult(r *Redactor, result Result) Result {
	result.Error = r.Redact(result.Error)
	if result.Verdict != nil {
		result.Verdict.Message = r.Redact(result.Verdict.Message)
		result.Verdict.Evidence = redactJSONPreserve(r, result.Verdict.Evidence)
	}
	for i := range result.Branches {
		result.Branches[i] = redactBranch(r, result.Branches[i])
	}
	for i := range result.Attempts {
		result.Attempts[i] = redactAttempt(r, result.Attempts[i])
	}
	for i := range result.Observations {
		result.Observations[i] = redactObservation(r, result.Observations[i])
	}
	for i := range result.Knowledge {
		result.Knowledge[i] = redactKnowledge(r, result.Knowledge[i])
	}
	for i := range result.Artifacts {
		result.Artifacts[i] = redactArtifact(r, result.Artifacts[i])
	}
	for i := range result.Sessions {
		result.Sessions[i].Error = r.Redact(result.Sessions[i].Error)
	}
	return result
}
