package engine

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/red-copilot/harness/core"
)

var ErrRunNotFound = errors.New("run not found")

func isTerminal(status core.Status) bool {
	switch status {
	case core.StatusSucceeded, core.StatusFailed, core.StatusCancelled, core.StatusTimedOut, core.StatusInterrupted, core.StatusStagnated:
		return true
	}
	return false
}

func randomID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

func ensureDir(path string) error { return os.MkdirAll(path, 0o700) }

func mustJSON(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

func redactError(r *core.Redactor, err error) string {
	if err == nil {
		return ""
	}
	return r.Redact(err.Error())
}

const DefaultToolCallTimeout = 30 * time.Second

func buildEnvironment(runID, sessionID string, spec RunSpec, agent AgentSpec, target core.TargetBinding, workspaceDir, taskDir, skillsDir string) map[string]string {
	env := map[string]string{"HARNESS_RUN_ID": runID, "HARNESS_SESSION_ID": sessionID, "HARNESS_TARGET_ID": target.TargetID, "HARNESS_WORKSPACE": workspaceDir, "HARNESS_TASK_DIR": taskDir, "HARNESS_SKILLS_DIR": skillsDir, "HOME": "/workspace"}
	for key, value := range agent.Env {
		env[key] = value
	}
	for _, key := range agent.EnvFrom {
		if value, ok := os.LookupEnv(key); ok {
			env[key] = value
		}
	}
	for _, key := range spec.SecretEnv {
		if value, ok := os.LookupEnv(key); ok {
			env[key] = value
		}
	}
	return env
}
