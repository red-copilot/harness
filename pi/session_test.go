package pi_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/pi"
)

func TestPromptWaitsForAgentSettledNotAgentEnd(t *testing.T) {
	h := newRPCHarness(t)
	session := h.session(t)

	if err := session.Prompt(context.Background(), "hello"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	h.write(t, `{"type":"agent_end","messages":[],"willRetry":false}`)
	waitDone := make(chan struct{})
	go func() {
		_, _ = session.Wait(context.Background())
		close(waitDone)
	}()
	select {
	case <-waitDone:
		t.Fatal("Wait returned on agent_end")
	case <-time.After(30 * time.Millisecond):
	}
	h.write(t, `{"type":"agent_settled","aborted":false}`)
	select {
	case <-waitDone:
	case <-time.After(time.Second):
		t.Fatal("Wait did not return on agent_settled")
	}
}

func TestResponseCorrelationAndUnicodeLineSeparators(t *testing.T) {
	h := newRPCHarness(t)
	session := h.session(t)
	h.onCommand = func(cmd map[string]any) {
		if cmd["type"] == "prompt" {
			h.write(t, `{"id":"`+cmd["id"].(string)+`","type":"response","command":"prompt","success":true,"data":{"disposition":"started"}}`)
			h.write(t, "{\"type\":\"message_update\",\"assistantMessageEvent\":{\"type\":\"text_delta\",\"delta\":\"a\\u2028b\\u2029c\"}}\n")
			h.write(t, `{"type":"agent_settled","aborted":false}`)
		}
	}
	if err := session.Prompt(context.Background(), "hello"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	var saw bool
	for event := range session.Events() {
		if strings.Contains(string(event.Data), `\u2028`) && strings.Contains(string(event.Data), `\u2029`) {
			saw = true
			break
		}
		if event.Type == "agent_settled" {
			break
		}
	}
	if !saw {
		t.Fatal("did not observe unicode-separated event payload")
	}
}

func TestSafePiCommandDefaults(t *testing.T) {
	args := pi.BuildCommand(harness.AgentSpec{
		ID: "pi", Image: "kali-pi:test",
		Pi: &harness.PiSpec{
			Provider: "openai", Model: "gpt-test", Thinking: "medium",
			Tools: []string{"read", "bash"}, EnvPassthrough: []string{"OPENAI_API_KEY"},
		},
	}, "/skills/tsec-submit")
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"pi --mode rpc", "--no-session", "--no-approve", "--no-context-files",
		"--no-extensions", "--no-mcp", "--no-prompt-templates", "--no-themes",
		"--no-skills", "--skill /skills/tsec-submit", "--tools read,bash",
		"--provider openai", "--model gpt-test", "--thinking medium", "--offline",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("command %q missing %q", joined, want)
		}
	}
}

func TestMalformedRecordDoesNotKillSession(t *testing.T) {
	h := newRPCHarness(t)
	session := h.session(t)
	h.write(t, "not json\n")
	h.write(t, `{"type":"agent_settled","aborted":false}`)
	result, err := session.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Status != harness.StatusSucceeded {
		t.Fatalf("status = %q", result.Status)
	}
}

type rpcHarness struct {
	t         *testing.T
	proc      *fakeProcess
	starter   *fakeStarter
	onCommand func(map[string]any)
	writeMu   sync.Mutex
}

func newRPCHarness(t *testing.T) *rpcHarness {
	t.Helper()
	h := &rpcHarness{t: t}
	h.proc = newFakeProcess()
	h.starter = &fakeStarter{proc: h.proc}
	go func() {
		dec := json.NewDecoder(h.proc.stdinReader)
		for {
			var cmd map[string]any
			if err := dec.Decode(&cmd); err != nil {
				return
			}
			if h.onCommand != nil {
				h.onCommand(cmd)
			} else {
				h.write(t, `{"id":"`+cmd["id"].(string)+`","type":"response","command":"`+cmd["type"].(string)+`","success":true,"data":{"disposition":"started"}}`)
			}
		}
	}()
	return h
}

func (h *rpcHarness) session(t *testing.T) harness.AgentSession {
	t.Helper()
	factory := pi.NewFactory()
	session, err := factory.NewSession(context.Background(), harness.SessionRequest{
		RunID: "run", SessionID: "s1",
		Agent: harness.AgentSpec{
			ID: "pi", Image: "kali-pi:test",
			Pi: &harness.PiSpec{Provider: "openai", Model: "gpt-test", Tools: []string{"read", "bash"}},
		},
		Task:         harness.Task{ID: "t", Objective: "test"},
		WorkspaceDir: t.TempDir(), TaskDir: t.TempDir(), SkillsDir: t.TempDir(),
		Env: map[string]string{}, Starter: h.starter,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return session
}

func (h *rpcHarness) write(t *testing.T, line string) {
	t.Helper()
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	if _, err := io.WriteString(h.proc.stdoutWriter, line+"\n"); err != nil {
		t.Fatalf("write stdout: %v", err)
	}
}

type fakeStarter struct {
	proc *fakeProcess
	spec harness.ProcessSpec
}

func (s *fakeStarter) Start(_ context.Context, spec harness.ProcessSpec) (harness.Process, error) {
	s.spec = spec
	return s.proc, nil
}

type fakeProcess struct {
	stdinReader  *io.PipeReader
	stdinWriter  *io.PipeWriter
	stdoutReader *io.PipeReader
	stdoutWriter *io.PipeWriter
	stderrReader *io.PipeReader
	stderrWriter *io.PipeWriter
	wait         chan struct{}
	once         sync.Once
}

func newFakeProcess() *fakeProcess {
	sr, sw := io.Pipe()
	or, ow := io.Pipe()
	er, ew := io.Pipe()
	return &fakeProcess{
		stdinReader: sr, stdinWriter: sw, stdoutReader: or, stdoutWriter: ow,
		stderrReader: er, stderrWriter: ew, wait: make(chan struct{}),
	}
}
func (p *fakeProcess) Stdin() io.WriteCloser { return p.stdinWriter }
func (p *fakeProcess) Stdout() io.Reader     { return p.stdoutReader }
func (p *fakeProcess) Stderr() io.Reader     { return p.stderrReader }
func (p *fakeProcess) Wait(context.Context) (harness.ExitStatus, error) {
	<-p.wait
	return harness.ExitStatus{Code: 0}, nil
}
func (p *fakeProcess) Kill(context.Context) error {
	p.once.Do(func() {
		close(p.wait)
		_ = p.stdoutWriter.Close()
		_ = p.stderrWriter.Close()
	})
	return nil
}
func (p *fakeProcess) exit() {
	p.once.Do(func() {
		close(p.wait)
		_ = p.stdoutWriter.Close()
		_ = p.stderrWriter.Close()
	})
}
