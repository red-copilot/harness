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
}

func (f *frontierFactory) NewSession(_ context.Context, req harness.SessionRequest) (harness.AgentSession, error) {
	session := &frontierSession{
		role: req.SessionID, workspace: req.WorkspaceDir,
		events: make(chan harness.AgentEvent, 8), blockPrompt: f.blockExecutors && req.SessionID != "planner",
	}
	f.mu.Lock()
	f.roles = append(f.roles, req.SessionID)
	f.sessions = append(f.sessions, session)
	f.mu.Unlock()
	return session, nil
}

type frontierSession struct {
	role        string
	workspace   string
	events      chan harness.AgentEvent
	mu          sync.Mutex
	resets      int
	closed      bool
	closeOnce   sync.Once
	blockPrompt bool
}

func (s *frontierSession) Prompt(ctx context.Context, message string) error {
	if s.blockPrompt {
		<-ctx.Done()
		return ctx.Err()
	}
	if strings.HasPrefix(s.role, "planner") {
		payload := `{"next_items":[{"question":"Inspect the HTTP root","target":"web","expected_evidence":"HTTP status and headers"}]}`
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
	if len(roles) != 2 || roles[0] != "planner" || roles[1] != "executor-1" {
		t.Fatalf("roles = %v, want planner + executor-1", roles)
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
