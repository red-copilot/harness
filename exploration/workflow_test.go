package exploration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/red-copilot/harness"
)

type workflowTestRuntime struct {
	agents     []harness.AgentSpec
	sessions   map[string]harness.AgentSession
	workspace  string
	mu         sync.Mutex
	opened     []string
	events     []harness.Event
	failPrefix string
}

func (r *workflowTestRuntime) RunID() string { return "run-test" }
func (r *workflowTestRuntime) Task() harness.Task {
	return harness.Task{ID: "task-test", Objective: "solve"}
}
func (r *workflowTestRuntime) Target() harness.Target {
	return harness.Target{ID: "target", Addresses: []string{"127.0.0.1:8080"}}
}
func (r *workflowTestRuntime) Agents() []harness.AgentSpec {
	return append([]harness.AgentSpec(nil), r.agents...)
}
func (r *workflowTestRuntime) Budget() harness.Budget { return harness.Budget{} }
func (r *workflowTestRuntime) WorkspaceRoot() string  { return r.workspace }
func (r *workflowTestRuntime) Progress() harness.Completion {
	return harness.Completion{}
}
func (r *workflowTestRuntime) Redact(value string) string { return value }

func (r *workflowTestRuntime) OpenSession(_ context.Context, agent harness.AgentSpec) (harness.SessionLease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[agent.ID]
	if !ok {
		return harness.SessionLease{}, errors.New("unexpected session " + agent.ID)
	}
	r.opened = append(r.opened, agent.ID)
	return harness.SessionLease{ID: agent.ID, Agent: agent, WorkspaceDir: r.workspace, Session: session}, nil
}

func (r *workflowTestRuntime) Emit(_ context.Context, event harness.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failPrefix != "" && strings.HasPrefix(event.Type, r.failPrefix) {
		return errors.New("event rejected")
	}
	r.events = append(r.events, event)
	return nil
}

type scriptedSession struct {
	mu         sync.Mutex
	step       int
	events     [][]harness.AgentEvent
	prompts    []func() error
	usage      harness.Usage
	current    chan harness.AgentEvent
	promptText func(string) error
}

func (s *scriptedSession) Prompt(_ context.Context, prompt string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.promptText != nil {
		if err := s.promptText(prompt); err != nil {
			return err
		}
	}
	if s.step < len(s.prompts) && s.prompts[s.step] != nil {
		if err := s.prompts[s.step](); err != nil {
			return err
		}
	}
	index := s.step
	if index >= len(s.events) {
		index = len(s.events) - 1
	}
	ch := make(chan harness.AgentEvent, 16)
	if index >= 0 {
		for _, event := range s.events[index] {
			ch <- event
		}
	}
	ch <- harness.AgentEvent{Type: "agent_settled"}
	close(ch)
	s.current = ch
	s.step++
	return nil
}
func (s *scriptedSession) Steer(context.Context, string) error { return nil }
func (s *scriptedSession) Events() <-chan harness.AgentEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		ch := make(chan harness.AgentEvent)
		close(ch)
		s.current = ch
	}
	return s.current
}
func (s *scriptedSession) Wait(context.Context) (harness.AgentResult, error) {
	return harness.AgentResult{Status: harness.StatusSucceeded}, nil
}
func (s *scriptedSession) Usage() harness.Usage        { return s.usage }
func (s *scriptedSession) Abort(context.Context) error { return nil }
func (s *scriptedSession) Close(context.Context) error { return nil }
func (s *scriptedSession) Reset(context.Context) error {
	s.mu.Lock()
	s.step = 0
	s.current = nil
	s.mu.Unlock()
	return nil
}

func TestWorkflowRunsPlannerAndExecutorThroughRuntime(t *testing.T) {
	workspace := t.TempDir()
	planner := &scriptedSession{
		events: [][]harness.AgentEvent{{
			turnEnd(`{"create_branches":[{"topic":"web.oa","goal":"find oa","hypothesis":"oa is reachable","success_condition":"oa responds","failure_condition":"oa does not respond","target":"target"}]}`),
		}},
	}
	executor := &scriptedSession{
		promptText: func(prompt string) error {
			branchID := branchIDFromPrompt(prompt)
			if branchID == "" {
				return errors.New("branch id missing from executor prompt")
			}
			return writeWorkflowReport(filepath.Join(workspace, "report.json"), map[string]any{
				"branch_id": branchID,
				"status":    "completed",
				"outcome":   "local_success",
				"observations": []map[string]any{{
					"kind": "http", "summary": "HTTP 404", "evidence_refs": []string{"raw-1"},
				}},
			})
		},
	}

	rt := &workflowTestRuntime{
		agents: []harness.AgentSpec{{ID: "pi", Pi: &harness.PiSpec{}}},
		sessions: map[string]harness.AgentSession{
			"planner":    planner,
			"executor-1": executor,
		},
		workspace: workspace,
	}
	policy := DefaultPolicy()
	policy.MaxNodes = 3
	policy.StagnationRounds = 1
	result, err := NewWorkflow(policy, nil).Execute(context.Background(), rt)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Stagnated {
		t.Fatalf("result = %#v, want stagnated", result)
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if len(rt.opened) < 2 || rt.opened[0] != "planner" || rt.opened[1] != "executor-1" {
		t.Fatalf("opened sessions = %#v", rt.opened)
	}
}

func TestWorkflowStopsWhenControlEventFails(t *testing.T) {
	workspace := t.TempDir()
	planner := &scriptedSession{events: [][]harness.AgentEvent{{
		turnEnd(`{"create_branches":[{"topic":"web.oa","goal":"oa","hypothesis":"oa","success_condition":"yes","failure_condition":"no","target":"target"}]}`),
	}}}

	rt := &workflowTestRuntime{
		agents:     []harness.AgentSpec{{ID: "pi"}},
		sessions:   map[string]harness.AgentSession{"planner": planner},
		workspace:  workspace,
		failPrefix: "exploration.branch.created",
	}
	policy := DefaultPolicy()
	policy.MaxNodes = 2
	_, err := NewWorkflow(policy, nil).Execute(context.Background(), rt)
	if err == nil || !strings.Contains(err.Error(), "event rejected") {
		t.Fatalf("Execute error = %v, want event rejection", err)
	}
}

func TestConfigureInstallsExplorationWorkflow(t *testing.T) {
	spec := &harness.RunSpec{Agents: []harness.AgentSpec{{ID: "pi"}}}
	if err := Configure(spec, DefaultPolicy(), nil); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if spec.Workflow == nil {
		t.Fatal("workflow not configured")
	}
	if spec.WorkflowConfig == nil || spec.WorkflowConfig.Name != "exploration" {
		t.Fatalf("workflow config = %#v", spec.WorkflowConfig)
	}
	if !json.Valid(spec.WorkflowConfig.Config) {
		t.Fatalf("workflow config JSON is invalid: %s", spec.WorkflowConfig.Config)
	}
}

func turnEnd(text string) harness.AgentEvent {
	data, _ := json.Marshal(map[string]any{"message": map[string]any{"content": []map[string]any{{
		"type": "text", "text": text,
	}}}})
	return harness.AgentEvent{Type: "turn_end", Data: data}
}

func writeWorkflowReport(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

var _ = time.Second

func branchIDFromPrompt(prompt string) string {
	for _, line := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(line, "Branch: ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Branch: "))
		}
	}
	return ""
}

type recordingStore struct {
	branch      Branch
	attempt     Attempt
	observation Observation
	knowledge   Knowledge
}

func (s *recordingStore) ListKnowledge(context.Context, string) ([]Knowledge, error) {
	return nil, nil
}
func (s *recordingStore) SaveKnowledge(_ context.Context, item Knowledge) error {
	s.knowledge = item
	return nil
}
func (s *recordingStore) UpdateKnowledge(_ context.Context, item Knowledge) error {
	s.knowledge = item
	return nil
}
func (s *recordingStore) SaveBranch(_ context.Context, item Branch) error {
	s.branch = item
	return nil
}
func (s *recordingStore) SaveAttempt(_ context.Context, item Attempt) error {
	s.attempt = item
	return nil
}
func (s *recordingStore) SaveObservation(_ context.Context, item Observation) error {
	s.observation = item
	return nil
}

func TestRedactingStoreRedactsNormalizedText(t *testing.T) {
	store := &recordingStore{}
	redacted := &redactingStore{
		store: store,
		redact: func(value string) string {
			return strings.ReplaceAll(value, "secret", "[REDACTED]")
		},
	}
	if err := redacted.SaveBranch(context.Background(), Branch{Goal: "secret branch"}); err != nil {
		t.Fatal(err)
	}
	if err := redacted.SaveObservation(context.Background(), Observation{Summary: "secret observation"}); err != nil {
		t.Fatal(err)
	}
	if err := redacted.SaveKnowledge(context.Background(), Knowledge{Statement: "secret knowledge"}); err != nil {
		t.Fatal(err)
	}
	if store.branch.Goal != "[REDACTED] branch" {
		t.Fatalf("branch goal = %q", store.branch.Goal)
	}
	if store.observation.Summary != "[REDACTED] observation" {
		t.Fatalf("observation summary = %q", store.observation.Summary)
	}
	if store.knowledge.Statement != "[REDACTED] knowledge" {
		t.Fatalf("knowledge statement = %q", store.knowledge.Statement)
	}
}
