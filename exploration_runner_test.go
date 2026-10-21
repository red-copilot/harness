package harness_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/red-copilot/harness"
)

type frontierFactory struct {
	mu             sync.Mutex
	roles          []string
	sessions       []*frontierSession
	blockExecutors bool
	plannerStarted chan struct{}
	plannerGate    <-chan struct{}
}

func (f *frontierFactory) NewSession(_ context.Context, req harness.SessionRequest) (harness.AgentSession, error) {
	session := &frontierSession{
		role: req.SessionID, workspace: req.WorkspaceDir,
		events: make(chan harness.AgentEvent, 8), blockPrompt: f.blockExecutors && req.SessionID != "planner",
		plannerStarted: f.plannerStarted, plannerGate: f.plannerGate,
	}
	f.mu.Lock()
	f.roles = append(f.roles, req.SessionID)
	f.sessions = append(f.sessions, session)
	f.mu.Unlock()
	return session, nil
}

type frontierSession struct {
	role             string
	workspace        string
	events           chan harness.AgentEvent
	mu               sync.Mutex
	resets           int
	closed           bool
	closeOnce        sync.Once
	blockPrompt      bool
	prompts          []string
	plannerStarted   chan struct{}
	plannerGate      <-chan struct{}
	plannerStartOnce sync.Once
}

func (s *frontierSession) Prompt(ctx context.Context, message string) error {
	s.mu.Lock()
	s.prompts = append(s.prompts, message)
	s.mu.Unlock()
	if s.blockPrompt {
		<-ctx.Done()
		return ctx.Err()
	}
	if strings.HasPrefix(s.role, "planner") {
		s.plannerStartOnce.Do(func() {
			if s.plannerStarted != nil {
				close(s.plannerStarted)
			}
		})
		if s.plannerGate != nil {
			<-s.plannerGate
		}
		payload := `{"next_items":[{"question":"Inspect the HTTP root","target":"web","expected_evidence":"HTTP status and headers"},{"question":"Inspect the admin path","target":"web","expected_evidence":"admin response"}]}`
		data, _ := json.Marshal(map[string]any{
			"type": "turn_end",
			"message": map[string]any{
				"role":    "assistant",
				"content": []map[string]any{{"type": "text", "text": payload}},
			},
		})
		s.events <- harness.AgentEvent{Type: "turn_end", Data: data}
		s.events <- harness.AgentEvent{Type: "agent_settled", Data: json.RawMessage(`{"type":"agent_settled"}`)}
		return nil
	}
	itemID := regexp.MustCompile(`(?m)^- id: (.+)$`).FindStringSubmatch(message)
	if len(itemID) != 2 {
		return os.ErrInvalid
	}
	report := harness.ExplorerReport{
		ItemID:  strings.TrimSpace(itemID[1]),
		Verdict: harness.VerdictInconclusive,
		Claims: []harness.ExplorerClaim{{
			Claim: "HTTP root is reachable", Evidence: "GET / returned 200", Targets: []string{"web"},
		}},
	}
	data, _ := json.Marshal(report)
	if err := os.WriteFile(filepath.Join(s.workspace, harness.ReportFileName), data, 0o600); err != nil {
		return err
	}
	s.events <- harness.AgentEvent{Type: "agent_settled", Data: json.RawMessage(`{"type":"agent_settled"}`)}
	return nil
}

func (s *frontierSession) Steer(context.Context, string) error { return nil }
func (s *frontierSession) Events() <-chan harness.AgentEvent   { return s.events }
func (s *frontierSession) Wait(context.Context) (harness.AgentResult, error) {
	return harness.AgentResult{Status: harness.StatusSucceeded}, nil
}
func (s *frontierSession) Abort(context.Context) error { return nil }
func (s *frontierSession) Close(context.Context) error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.events)
	})
	return nil
}
func (s *frontierSession) Reset(context.Context) error {
	s.mu.Lock()
	s.resets++
	s.mu.Unlock()
	return nil
}
func (s *frontierSession) Usage() harness.Usage {
	return harness.Usage{TotalTokens: 10, ModelCalls: 1, ToolCalls: 1}
}

func TestRunnerDispatchesFreshFrontierSessions(t *testing.T) {
	store := newMemoryStore()
	factory := &frontierFactory{}
	runner, err := harness.NewRunner(harness.Config{
		Store: store, DefaultFactory: factory, WorkspaceRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	result, err := runner.Run(context.Background(), harness.RunSpec{
		Task:   harness.Task{ID: "frontier", Objective: "find the flag"},
		Target: harness.Target{ID: "web", Addresses: []string{"10.0.0.5:80"}},
		Agents: []harness.AgentSpec{{ID: "base"}},
		Exploration: &harness.ExplorationSpec{
			Rounds: 1,
		},
		Verifier: fakeVerifier{verdict: harness.Verdict{Success: true, Message: "verified"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != harness.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", result.Status)
	}
	factory.mu.Lock()
	roles := append([]string(nil), factory.roles...)
	sessions := append([]*frontierSession(nil), factory.sessions...)
	factory.mu.Unlock()
	if len(roles) != 3 || roles[0] != "planner" || roles[1] != "executor-1" || roles[2] != "executor-2" {
		t.Fatalf("roles = %v, want planner + executor-1 + executor-2", roles)
	}
	for _, session := range sessions {
		session.mu.Lock()
		resets := session.resets
		closed := session.closed
		session.mu.Unlock()
		if resets != 1 {
			t.Fatalf("session %s resets = %d, want 1", session.role, resets)
		}
		if !closed {
			t.Fatalf("session %s was not closed", session.role)
		}
	}
	events, err := store.ListEvents(context.Background(), result.RunID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var sawAssign, sawFacts bool
	for _, event := range events {
		switch event.Type {
		case "exploration.item.assigned":
			sawAssign = true
		case "exploration.facts.merged":
			sawFacts = true
		}
	}
	if !sawAssign || !sawFacts {
		t.Fatalf("missing exploration events assign=%v facts=%v", sawAssign, sawFacts)
	}
}

func TestRunnerStartsExecutorOnlyAfterPlannerProducesFrontier(t *testing.T) {
	store := newMemoryStore()
	gate := make(chan struct{})
	factory := &frontierFactory{
		plannerStarted: make(chan struct{}),
		plannerGate:    gate,
	}
	runner, err := harness.NewRunner(harness.Config{
		Store: store, DefaultFactory: factory, WorkspaceRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	type outcome struct {
		result harness.Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, runErr := runner.Run(context.Background(), harness.RunSpec{
			Task:        harness.Task{ID: "lazy-executor", Objective: "find the flag"},
			Target:      harness.Target{ID: "web", Addresses: []string{"10.0.0.5:80"}},
			Agents:      []harness.AgentSpec{{ID: "base"}},
			Exploration: &harness.ExplorationSpec{Rounds: 1},
			Verifier:    fakeVerifier{verdict: harness.Verdict{Success: true}},
		})
		done <- outcome{result: result, err: runErr}
	}()
	select {
	case <-factory.plannerStarted:
	case <-time.After(time.Second):
		t.Fatal("planner did not start")
	}
	factory.mu.Lock()
	rolesBeforePlan := append([]string(nil), factory.roles...)
	factory.mu.Unlock()
	if len(rolesBeforePlan) != 1 || rolesBeforePlan[0] != "planner" {
		t.Fatalf("executor started before frontiers were ready: %v", rolesBeforePlan)
	}
	close(gate)
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("Run: %v", result.err)
		}
		if result.result.Status != harness.StatusSucceeded {
			t.Fatalf("status = %q", result.result.Status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run did not finish")
	}
	factory.mu.Lock()
	rolesAfterPlan := append([]string(nil), factory.roles...)
	factory.mu.Unlock()
	if len(rolesAfterPlan) != 3 || rolesAfterPlan[1] != "executor-1" || rolesAfterPlan[2] != "executor-2" {
		t.Fatalf("executor was not created after plan: %v", rolesAfterPlan)
	}
}

func TestRunnerRejectsUnknownExplorationRole(t *testing.T) {
	runner, err := harness.NewRunner(harness.Config{
		Store: newMemoryStore(), WorkspaceRoot: t.TempDir(),
		DefaultFactory: &frontierFactory{},
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	_, err = runner.Run(context.Background(), harness.RunSpec{
		Task:   harness.Task{ID: "bad-role", Objective: "test"},
		Agents: []harness.AgentSpec{{ID: "base"}},
		Exploration: &harness.ExplorationSpec{
			PlannerAgentID: "missing",
			Rounds:         1,
		},
		Verifier: fakeVerifier{verdict: harness.Verdict{Success: false}},
	})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("err = %v, want missing role error", err)
	}
}

func TestBackendCompletionPreemptsFrontierExecution(t *testing.T) {
	store := newMemoryStore()
	factory := &frontierFactory{blockExecutors: true}
	runner, err := harness.NewRunner(harness.Config{
		Store: store, DefaultFactory: factory, WorkspaceRoot: t.TempDir(),
		PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	result, err := runner.Run(context.Background(), harness.RunSpec{
		Task:        harness.Task{ID: "backend-preempt", Objective: "solve"},
		Target:      harness.Target{ID: "web", Addresses: []string{"10.0.0.5:80"}},
		Agents:      []harness.AgentSpec{{ID: "base"}},
		Exploration: &harness.ExplorationSpec{Rounds: 6},
		Backend: &fakeBackend{completion: harness.Completion{
			Done: true, Success: true, Score: 500, Progress: 6, Total: 6,
		}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != harness.StatusSucceeded || result.Verdict == nil || result.Verdict.Score != 500 {
		t.Fatalf("result = %#v", result)
	}
}

func TestPlannerReceivesAuthoritativeBackendProgress(t *testing.T) {
	store := newMemoryStore()
	factory := &frontierFactory{}
	runner, err := harness.NewRunner(harness.Config{
		Store: store, DefaultFactory: factory, WorkspaceRoot: t.TempDir(),
		PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	backend := &stagedBackend{observations: []harness.Completion{
		{Progress: 2, Total: 6},
		{Progress: 2, Total: 6},
		{Done: true, Success: true, Progress: 6, Total: 6},
	}}
	result, err := runner.Run(context.Background(), harness.RunSpec{
		Task:        harness.Task{ID: "progress", Objective: "solve all flags"},
		Target:      harness.Target{ID: "web", Addresses: []string{"10.0.0.5:80"}},
		Agents:      []harness.AgentSpec{{ID: "base"}},
		Exploration: &harness.ExplorationSpec{Rounds: 1},
		Backend:     backend,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != harness.StatusSucceeded {
		t.Fatalf("status = %q", result.Status)
	}
	factory.mu.Lock()
	sessions := append([]*frontierSession(nil), factory.sessions...)
	factory.mu.Unlock()
	for _, session := range sessions {
		if session.role != "planner" {
			continue
		}
		session.mu.Lock()
		prompts := append([]string(nil), session.prompts...)
		session.mu.Unlock()
		if len(prompts) == 0 || !strings.Contains(prompts[0], "Authoritative progress: 2/6") {
			t.Fatalf("planner prompt missing authoritative progress:\n%v", prompts)
		}
		return
	}
	t.Fatal("planner session not created")
}

type stagedBackend struct {
	mu           sync.Mutex
	observations []harness.Completion
	index        int
}

func (b *stagedBackend) Provision(context.Context, harness.ProvisionRequest) (harness.Target, error) {
	return harness.Target{ID: "web", Addresses: []string{"10.0.0.5:80"}}, nil
}

func (b *stagedBackend) Observe(context.Context, harness.Target) (harness.Completion, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.index >= len(b.observations) {
		return b.observations[len(b.observations)-1], nil
	}
	completion := b.observations[b.index]
	b.index++
	return completion, nil
}

func (b *stagedBackend) Close(context.Context, harness.Target) error { return nil }
