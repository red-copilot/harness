package verify_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/verify"
)

func TestCommandVerifierPassesJSONAndReturnsVerdict(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "verify.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
read -r line
printf '%s' "$line" > "$CAPTURE"
printf '{"success":true,"score":42,"message":"verified","evidence":[{"kind":"flag"}]}'
`), 0o700); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(dir, "request.json")
	verifier := verify.Command{Path: script, Env: []string{"CAPTURE=" + capture}}
	verdict, err := verifier.Verify(context.Background(), harness.VerifyRequest{
		RunID: "run_1", Task: harness.Task{ID: "task", Objective: "solve"}, EvidenceDir: dir,
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !verdict.Success || verdict.Score != 42 {
		t.Fatalf("verdict = %#v", verdict)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatalf("request JSON: %v: %s", err, data)
	}
	if request["run_id"] != "run_1" {
		t.Fatalf("request = %#v", request)
	}
}

func TestCommandVerifierFailsClosed(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "verify.sh")
	_ = os.WriteFile(script, []byte("#!/bin/sh\ncat >/dev/null\nexit 3\n"), 0o700)
	if _, err := (verify.Command{Path: script}).Verify(context.Background(), harness.VerifyRequest{}); err == nil {
		t.Fatal("expected verifier process error")
	}
	_ = os.WriteFile(script, []byte("#!/bin/sh\ncat >/dev/null\necho not-json\n"), 0o700)
	if _, err := (verify.Command{Path: script}).Verify(context.Background(), harness.VerifyRequest{}); err == nil {
		t.Fatal("expected invalid verdict error")
	}
}

func TestCommandVerifierRejectsMissingScript(t *testing.T) {
	if _, err := (verify.Command{Path: filepath.Join(t.TempDir(), "missing")}).Verify(context.Background(), harness.VerifyRequest{}); err == nil {
		t.Fatal("missing verifier script was accepted")
	}
}
