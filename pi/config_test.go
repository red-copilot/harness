package pi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/red-copilot/harness"
)

func TestFactoryPlannerUsesReadOnlyTools(t *testing.T) {
	command := BuildCommand(harness.AgentSpec{ID: "planner"}, harness.AgentRolePlanner, Config{})
	joined := strings.Join(command, " ")
	if !strings.Contains(joined, "--tools read") || strings.Contains(joined, "bash") {
		t.Fatalf("planner command = %v", command)
	}
}

func TestFactoryExecutorUsesConfiguredOrDefaultShellTools(t *testing.T) {
	command := BuildCommand(harness.AgentSpec{ID: "executor"}, harness.AgentRoleExecutor, Config{})
	joined := strings.Join(command, " ")
	if !strings.Contains(joined, "--tools read,bash,edit,write") {
		t.Fatalf("executor command = %v", command)
	}
	command = BuildCommand(harness.AgentSpec{ID: "executor"}, harness.AgentRoleExecutor, Config{Tools: []string{"read", "bash"}})
	if !strings.Contains(strings.Join(command, " "), "--tools read,bash") {
		t.Fatalf("configured tools = %v", command)
	}
}

func TestFactoryRejectsInvalidModelsJSON(t *testing.T) {
	starter := &recordingStarter{}
	_, err := NewFactory().NewSession(context.Background(), harness.RuntimeRequest{RunID: "run", SessionID: "planner", Role: harness.AgentRolePlanner, Agent: harness.AgentSpec{ID: "planner", RuntimeConfig: []byte(`{"models_json":"not-json"}`)}, WorkspaceDir: t.TempDir(), TaskDir: t.TempDir(), SkillsDir: t.TempDir(), Starter: starter})
	if err == nil {
		t.Fatal("expected invalid models JSON error")
	}
	if starter.starts != 0 {
		t.Fatalf("process started %d times", starter.starts)
	}
}

func TestFactoryNeverReceivesDockerSocket(t *testing.T) {
	starter := &recordingStarter{}
	_, err := NewFactory().NewSession(context.Background(), harness.RuntimeRequest{RunID: "run", SessionID: "executor", Role: harness.AgentRoleExecutor, Agent: harness.AgentSpec{ID: "executor", Image: "image"}, WorkspaceDir: t.TempDir(), TaskDir: t.TempDir(), SkillsDir: t.TempDir(), Starter: starter})
	if err != nil {
		t.Fatal(err)
	}
	if starter.spec.Image != "image" {
		t.Fatalf("spec = %+v", starter.spec)
	}
	for _, mount := range starter.spec.Mounts {
		if strings.Contains(mount.Source, "docker.sock") || strings.Contains(mount.Destination, "docker.sock") {
			t.Fatalf("docker socket mounted: %+v", mount)
		}
	}
	if !starter.spec.ReadOnlyRoot {
		t.Fatal("root filesystem must be read-only")
	}
}

func TestPiStrictLFJSONL(t *testing.T) {
	proc := newTestProcess([]byte("{\"type\":\"agent_end\"}\r"))
	session := newSession(proc)
	defer session.Close(context.Background())
	select {
	case event := <-session.Events():
		if event.Type != "rpc.malformed" {
			t.Fatalf("event = %q", event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("CR-only framing was not reported malformed")
	}
}

func TestAgentEndDoesNotSettleButAgentSettledDoes(t *testing.T) {
	proc := newTestProcess([]byte("{\"type\":\"agent_end\"}\n{\"type\":\"agent_settled\"}\n"))
	session := newSession(proc)
	defer session.Close(context.Background())
	var types []string
	for len(types) < 2 {
		select {
		case event := <-session.Events():
			types = append(types, event.Type)
		case <-time.After(time.Second):
			t.Fatalf("events = %v", types)
		}
	}
	if types[0] != "agent_end" || types[1] != "agent_settled" {
		t.Fatalf("events = %v", types)
	}
}

func TestResetAllowsSecondTurn(t *testing.T) {
	proc := newScriptProcess()
	session := newSession(proc)
	defer session.Close(context.Background())
	if err := session.Prompt(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	waitEventType(t, session.Events(), "agent_settled")
	if err := session.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := session.Prompt(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	waitEventType(t, session.Events(), "agent_settled")
}

func waitEventType(t *testing.T, events <-chan harness.AgentEvent, want string) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatalf("events closed before %s", want)
			}
			if event.Type == want {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", want)
		}
	}
}

type recordingStarter struct {
	mu     sync.Mutex
	starts int
	spec   harness.ProcessSpec
}

func (s *recordingStarter) Start(_ context.Context, spec harness.ProcessSpec) (harness.Process, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starts++
	s.spec = spec
	return newTestProcess(nil), nil
}

type testProcess struct {
	stdin     io.WriteCloser
	stdout    io.Reader
	stderr    io.Reader
	wait      chan struct{}
	once      sync.Once
	mu        sync.Mutex
	exited    bool
	responses chan string
}

func newTestProcess(stdout []byte) *testProcess {
	return &testProcess{stdin: nopWriteCloser{}, stdout: bytes.NewReader(stdout), stderr: strings.NewReader(""), responses: make(chan string, 8)}
}
func newScriptProcess() *testProcess {
	responses := make(chan string, 16)
	return &testProcess{stdin: &recordingWriteCloser{out: responses}, stdout: &responseReader{in: responses}, stderr: strings.NewReader(""), responses: responses}
}
func (p *testProcess) Stdin() io.WriteCloser { return p.stdin }
func (p *testProcess) Stdout() io.Reader     { return p.stdout }
func (p *testProcess) Stderr() io.Reader     { return p.stderr }
func (p *testProcess) Wait(ctx context.Context) (harness.ExitStatus, error) {
	<-ctx.Done()
	return harness.ExitStatus{}, ctx.Err()
}
func (p *testProcess) Kill(context.Context) error { return nil }
func (p *testProcess) close()                     { p.once.Do(func() {}) }

type nopWriteCloser struct{}

func (nopWriteCloser) Write(data []byte) (int, error) { return len(data), nil }
func (nopWriteCloser) Close() error                   { return nil }

type responseReader struct {
	in     <-chan string
	buffer bytes.Buffer
}

func (r *responseReader) Read(data []byte) (int, error) {
	if r.buffer.Len() == 0 {
		line, ok := <-r.in
		if !ok {
			return 0, io.EOF
		}
		r.buffer.WriteString(line)
	}
	return r.buffer.Read(data)
}

type recordingWriteCloser struct {
	out    chan<- string
	closed bool
}

func (w *recordingWriteCloser) Write(data []byte) (int, error) {
	var command struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	_ = json.Unmarshal(data, &command)
	response := map[string]any{"type": "response", "id": command.ID, "command": command.Type, "success": true, "data": map[string]string{"disposition": "handled"}}
	encoded, _ := json.Marshal(response)
	w.out <- string(encoded) + "\n"
	if command.Type == "prompt" {
		w.out <- "{\"type\":\"agent_settled\"}\n"
	}
	return len(data), nil
}
func (w *recordingWriteCloser) Close() error {
	if !w.closed {
		w.closed = true
		close(w.out)
	}
	return nil
}

var _ harness.Process = (*testProcess)(nil)
var _ = errors.Is
