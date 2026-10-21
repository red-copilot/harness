package exploration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/red-copilot/harness"
)

// Store is the normalized-state persistence required by the exploration workflow.
type Store interface {
	KnowledgeStore
	BranchStore
}

// Workflow is the optional binary-tree exploration strategy.
type Workflow struct {
	policy Policy
	store  Store
}

// NewWorkflow constructs the optional exploration workflow.
func NewWorkflow(policy Policy, store Store) *Workflow {
	return &Workflow{policy: NormalizePolicy(policy), store: store}
}

// Configure installs an exploration workflow and its serializable audit
// descriptor on a run spec. The core treats the config as opaque data.
func Configure(spec *harness.RunSpec, policy Policy, store Store) error {
	if spec == nil {
		return fmt.Errorf("run spec is required")
	}
	policy = NormalizePolicy(policy)
	config, err := json.Marshal(policy)
	if err != nil {
		return fmt.Errorf("marshal exploration policy: %w", err)
	}
	spec.Workflow = NewWorkflow(policy, store)
	spec.WorkflowConfig = &harness.WorkflowConfig{Name: "exploration", Config: config}
	return nil
}

func (w *Workflow) Execute(ctx context.Context, rt harness.WorkflowRuntime) (harness.WorkflowResult, error) {
	if w == nil {
		return harness.WorkflowResult{}, fmt.Errorf("exploration workflow is required")
	}
	if rt == nil {
		return harness.WorkflowResult{}, fmt.Errorf("workflow runtime is required")
	}
	plannerSpec, executorSpecs, err := resolveAgents(rt.Agents(), w.policy)
	if err != nil {
		return harness.WorkflowResult{}, err
	}

	plannerLease, err := rt.OpenSession(ctx, plannerSpec)
	if err != nil {
		return harness.WorkflowResult{}, fmt.Errorf("start planner session: %w", err)
	}
	var workflowStore Store
	var knowledgeStore KnowledgeStore
	var branchStore BranchStore
	if w.store != nil {
		workflowStore = &redactingStore{store: w.store, redact: rt.Redact}
		knowledgeStore = workflowStore
		branchStore = workflowStore
	}

	planner := &SessionBranchPlanner{
		Session: plannerLease.Session,
		Sink:    sessionSink(ctx, rt, plannerLease.ID),
	}

	executors := make([]BranchExecutor, 0, len(executorSpecs))
	nextExecutor := 0
	acquireExecutors := func(acquireCtx context.Context, need int) ([]BranchExecutor, error) {
		if need > len(executorSpecs) {
			need = len(executorSpecs)
		}
		for len(executors) < need && nextExecutor < len(executorSpecs) {
			executorSpec := executorSpecs[nextExecutor]
			nextExecutor++
			lease, err := rt.OpenSession(acquireCtx, executorSpec)
			if err != nil {
				return nil, fmt.Errorf("start %s session: %w", executorSpec.ID, err)
			}
			executors = append(executors, &SessionBranchExecutor{
				Session: lease.Session, IDValue: lease.ID, WorkspaceDir: lease.WorkspaceDir,
				Sink: sessionSink(acquireCtx, rt, lease.ID),
			})
		}
		if len(executors) == 0 {
			return nil, fmt.Errorf("no branch executors available")
		}
		return append([]BranchExecutor(nil), executors...), nil
	}

	engine := NewBranchEngine(BranchEngineConfig{
		RunID: rt.RunID(), Task: rt.Task(), Targets: targetsFor(rt),
		Policy: w.policy, KnowledgeStore: knowledgeStore, Store: branchStore,
		Progress: func() ProgressSignal {
			completion := rt.Progress()
			return ProgressSignal{
				Authoritative: true, Source: "backend", Done: completion.Done,
				Success: completion.Success, Progress: completion.Progress, Total: completion.Total,
				Score: completion.Score, Message: completion.Message,
			}
		},
		OnStagnated:      func() {},
		AcquireExecutors: acquireExecutors,
	}, planner, func(event BranchEvent) error {
		eventType := event.Type
		if !strings.HasPrefix(eventType, "exploration.") {
			eventType = "exploration." + eventType
		}
		return rt.Emit(ctx, harness.Event{Source: "control", Type: eventType, Data: mustJSON(event.Data)})
	})
	if err := engine.Run(ctx); err != nil {
		return harness.WorkflowResult{}, err
	}
	return harness.WorkflowResult{Stagnated: engine.Stagnated()}, nil
}

func sessionSink(ctx context.Context, rt harness.WorkflowRuntime, sessionID string) EventSink {
	return func(event harness.AgentEvent) error {
		return rt.Emit(ctx, harness.Event{
			SessionID: sessionID, Source: "agent", Type: event.Type, Data: event.Data,
		})
	}
}

func resolveAgents(agents []harness.AgentSpec, policy Policy) (harness.AgentSpec, []harness.AgentSpec, error) {
	if len(agents) == 0 {
		return harness.AgentSpec{}, nil, fmt.Errorf("exploration requires at least one agent spec")
	}
	byID := make(map[string]harness.AgentSpec, len(agents))
	for _, agent := range agents {
		byID[agent.ID] = agent
	}
	base := agents[0]
	if policy.PlannerAgentID != "" {
		var ok bool
		base, ok = byID[policy.PlannerAgentID]
		if !ok {
			return harness.AgentSpec{}, nil, fmt.Errorf("exploration planner agent %q not found", policy.PlannerAgentID)
		}
	}
	planner := cloneAgentSpec(base)
	planner.ID = "planner"
	if planner.Pi == nil {
		planner.Pi = &harness.PiSpec{}
	}
	planner.Pi.Tools = []string{"read"}

	if len(policy.ExecutorAgentIDs) > 0 {
		executors := make([]harness.AgentSpec, 0, len(policy.ExecutorAgentIDs))
		for index, id := range policy.ExecutorAgentIDs {
			agent, ok := byID[id]
			if !ok {
				return harness.AgentSpec{}, nil, fmt.Errorf("exploration executor agent %q not found", id)
			}
			executor := cloneAgentSpec(agent)
			executor.ID = fmt.Sprintf("executor-%d", index+1)
			executors = append(executors, executor)
		}
		return planner, executors, nil
	}

	workers := policy.MaxWorkers
	if workers <= 0 {
		workers = policy.MaxActiveBranches
	}
	if workers <= 0 {
		workers = DefaultPolicy().MaxActiveBranches
	}
	executors := make([]harness.AgentSpec, 0, workers)
	for index := 1; index <= workers; index++ {
		executor := cloneAgentSpec(base)
		executor.ID = fmt.Sprintf("executor-%d", index)
		executors = append(executors, executor)
	}
	return planner, executors, nil
}

func cloneAgentSpec(agent harness.AgentSpec) harness.AgentSpec {
	clone := agent
	if agent.Pi != nil {
		piSpec := *agent.Pi
		piSpec.Tools = append([]string(nil), agent.Pi.Tools...)
		piSpec.ExtraArgs = append([]string(nil), agent.Pi.ExtraArgs...)
		piSpec.EnvPassthrough = append([]string(nil), agent.Pi.EnvPassthrough...)
		piSpec.Skills = append([]string(nil), agent.Pi.Skills...)
		clone.Pi = &piSpec
	}
	clone.Command = append([]string(nil), agent.Command...)
	clone.EnvFrom = append([]string(nil), agent.EnvFrom...)
	clone.CapAdd = append([]string(nil), agent.CapAdd...)
	clone.Env = make(map[string]string, len(agent.Env))
	for key, value := range agent.Env {
		clone.Env[key] = value
	}
	clone.Knowledge = append([]harness.Mount(nil), agent.Knowledge...)
	clone.Skills = append([]harness.Mount(nil), agent.Skills...)
	return clone
}

func targetsFor(rt harness.WorkflowRuntime) []harness.Target {
	targets := append([]harness.Target(nil), rt.Task().Targets...)
	target := rt.Target()
	if target.ID != "" || len(target.Addresses) > 0 {
		targets = append(targets, target)
	}
	return targets
}

func mustJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}

type redactingStore struct {
	store  Store
	redact func(string) string
}

func (s *redactingStore) redactText(value string) string {
	if s.redact == nil {
		return value
	}
	return s.redact(value)
}

func (s *redactingStore) redactList(values []string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = s.redactText(value)
	}
	return out
}

func (s *redactingStore) ListKnowledge(ctx context.Context, taskID string) ([]Knowledge, error) {
	return s.store.ListKnowledge(ctx, taskID)
}

func (s *redactingStore) SaveKnowledge(ctx context.Context, item Knowledge) error {
	item.Topic = s.redactText(item.Topic)
	item.Statement = s.redactText(item.Statement)
	item.Target = s.redactText(item.Target)
	item.EvidenceRefs = s.redactList(item.EvidenceRefs)
	item.Sources = s.redactList(item.Sources)
	item.Contradicts = s.redactList(item.Contradicts)
	return s.store.SaveKnowledge(ctx, item)
}

func (s *redactingStore) UpdateKnowledge(ctx context.Context, item Knowledge) error {
	item.Topic = s.redactText(item.Topic)
	item.Statement = s.redactText(item.Statement)
	item.Target = s.redactText(item.Target)
	item.EvidenceRefs = s.redactList(item.EvidenceRefs)
	item.Sources = s.redactList(item.Sources)
	item.Contradicts = s.redactList(item.Contradicts)
	return s.store.UpdateKnowledge(ctx, item)
}

func (s *redactingStore) SaveBranch(ctx context.Context, item Branch) error {
	item.Topic = s.redactText(item.Topic)
	item.Goal = s.redactText(item.Goal)
	item.Hypothesis = s.redactText(item.Hypothesis)
	item.SuccessCondition = s.redactText(item.SuccessCondition)
	item.FailureCondition = s.redactText(item.FailureCondition)
	item.Scope = s.redactText(item.Scope)
	return s.store.SaveBranch(ctx, item)
}

func (s *redactingStore) SaveAttempt(ctx context.Context, item Attempt) error {
	item.Error = s.redactText(item.Error)
	return s.store.SaveAttempt(ctx, item)
}

func (s *redactingStore) SaveObservation(ctx context.Context, item Observation) error {
	item.Kind = s.redactText(item.Kind)
	item.Action = s.redactText(item.Action)
	item.Target = s.redactText(item.Target)
	item.Summary = s.redactText(item.Summary)
	item.Data = []byte(s.redactText(string(item.Data)))
	item.EvidenceRefs = s.redactList(item.EvidenceRefs)
	return s.store.SaveObservation(ctx, item)
}
