package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/red-copilot/harness"
)

// Command is an external verifier contract. It receives one JSON object on
// stdin and must return a harness Verdict JSON object on stdout.
type Command struct {
	Path    string
	Args    []string
	Dir     string
	Env     []string
	Timeout time.Duration
}

func (c Command) Verify(ctx context.Context, req harness.VerifyRequest) (harness.Verdict, error) {
	if c.Path == "" {
		return harness.Verdict{}, errors.New("verifier command path is required")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	payload, err := json.Marshal(struct {
		RunID         string                  `json:"run_id"`
		Task          harness.Task            `json:"task"`
		Target        harness.Target          `json:"target"`
		Sessions      []harness.SessionResult `json:"sessions"`
		WorkspaceRoot string                  `json:"workspace_root"`
	}{
		RunID: req.RunID, Task: req.Task, Target: req.Target,
		Sessions: req.Sessions, WorkspaceRoot: req.WorkspaceRoot,
	})
	if err != nil {
		return harness.Verdict{}, err
	}
	cmd := exec.CommandContext(ctx, c.Path, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return harness.Verdict{}, fmt.Errorf("verifier process: %w: %s", err, detail)
		}
		return harness.Verdict{}, fmt.Errorf("verifier process: %w", err)
	}
	var verdict harness.Verdict
	if err := json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
		return harness.Verdict{}, fmt.Errorf("verifier returned invalid JSON: %w", err)
	}
	return verdict, nil
}

var _ harness.Verifier = Command{}
