package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type explorationSession struct {
	id      string
	agent   AgentSpec
	session AgentSession
	logDone chan struct{}
	planner bool
}

func resolveExplorationAgents(spec RunSpec) (AgentSpec, []AgentSpec, error) {
	if spec.Exploration == nil {
		return AgentSpec{}, nil, nil
	}
	if len(spec.Agents) == 0 {
		return AgentSpec{}, nil, fmt.Errorf("exploration requires at least one agent spec")
	}
	byID := make(map[string]AgentSpec, len(spec.Agents))
	for _, agent := range spec.Agents {
		byID[agent.ID] = agent
	}
	base := spec.Agents[0]
	if spec.Exploration.PlannerAgentID != "" {
		var ok bool
		base, ok = byID[spec.Exploration.PlannerAgentID]
		if !ok {
			return AgentSpec{}, nil, fmt.Errorf("exploration planner agent %q not found", spec.Exploration.PlannerAgentID)
		}
	}
	planner := cloneAgentSpec(base)
	planner.ID = "planner"
	if planner.Pi == nil {
		planner.Pi = &PiSpec{}
	}
	planner.Pi.Tools = []string{"read"}

	executorIDs := spec.Exploration.ExecutorAgentIDs
	if len(executorIDs) == 0 {
		executor := cloneAgentSpec(base)
		executor.ID = "executor-1"
		return planner, []AgentSpec{executor}, nil
	}
	executors := make([]AgentSpec, 0, len(executorIDs))
	for index, id := range executorIDs {
		agent, ok := byID[id]
		if !ok {
			return AgentSpec{}, nil, fmt.Errorf("exploration executor agent %q not found", id)
		}
		executor := cloneAgentSpec(agent)
		executor.ID = fmt.Sprintf("executor-%d", index+1)
		executors = append(executors, executor)
	}
	return planner, executors, nil
}

func explorationSessionCount(spec RunSpec) (int, error) {
	_, executors, err := resolveExplorationAgents(spec)
	if err != nil {
		return 0, err
	}
	return 1 + len(executors), nil
}

func cloneAgentSpec(agent AgentSpec) AgentSpec {
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
	clone.Knowledge = append([]Mount(nil), agent.Knowledge...)
	clone.Skills = append([]Mount(nil), agent.Skills...)
	return clone
}

// runExplorationSessions owns the role sessions for one run. It returns one
// SessionResult per planner/executor so the Runner audit record stays complete.
func (r *Runner) runExplorationSessions(
	ctx context.Context,
	run *Run,
	redactor *Redactor,
	spec RunSpec,
	target Target,
	runRoot string,
	taskDir string,
	skillsDir string,
	progress *completionTracker,
) []SessionResult {
	plannerSpec, executorSpecs, err := resolveExplorationAgents(spec)
	if err != nil {
		return []SessionResult{{ID: "planner", Status: StatusFailed, Error: redactor.Redact(err.Error())}}
	}

	var handles []*explorationSession
	plannerHandle, plannerErr := r.startExplorationSession(ctx, run, redactor, spec, plannerSpec, target, runRoot, taskDir, skillsDir)
	if plannerHandle != nil {
		handles = append(handles, plannerHandle)
	}
	var planner Planner
	if plannerHandle != nil {
		planner = &SessionPlanner{Session: plannerHandle.session, Sink: r.sessionEventSink(run, redactor, plannerHandle, spec.Budget)}
	}

	explorers := make([]Explorer, 0, len(executorSpecs))
	failedResults := make([]SessionResult, 0)
	for _, executorSpec := range executorSpecs {
		handle, startErr := r.startExplorationSession(ctx, run, redactor, spec, executorSpec, target, runRoot, taskDir, skillsDir)
		if startErr != nil {
			failedResults = append(failedResults, SessionResult{
				ID: executorSpec.ID, Status: StatusFailed, Error: redactor.Redact(startErr.Error()),
			})
			r.emitEvent(run, redactor, Event{
				RunID: run.ID, SessionID: executorSpec.ID, Source: "harness", Type: "session.failed",
				Time: time.Now().UTC(), Data: mustJSON(map[string]string{"error": redactor.Redact(startErr.Error())}),
			})
			continue
		}
		handles = append(handles, handle)
		explorers = append(explorers, &SessionExplorer{
			Session: handle.session, IDValue: handle.id, WorkspaceDir: sessionWorkspace(runRoot, handle.id),
			ToolLimit: 40, Sink: r.sessionEventSink(run, redactor, handle, spec.Budget),
		})
	}

	controllerCfg := ControllerConfig{
		Rounds: spec.Exploration.Rounds, Objective: spec.Task.Objective,
		Targets: taskTargets(spec.Task, target), MaxWorkers: spec.Exploration.MaxWorkers,
		Progress: progress.Snapshot,
	}
	controller := NewController(controllerCfg, planner, explorers, func(event ControllerEvent) {
		r.emitEvent(run, redactor, Event{
			RunID: run.ID, Source: "control", Type: "exploration." + event.Type,
			Time: time.Now().UTC(), Data: mustJSON(event.Data),
		})
	})
	controllerErr := controller.Run(ctx)

	results := make([]SessionResult, 0, len(handles)+1)
	if plannerHandle == nil {
		message := "planner session was not created"
		if plannerErr != nil {
			message = plannerErr.Error()
		}
		results = append(results, SessionResult{ID: "planner", Status: StatusFailed, Error: redactor.Redact(message)})
	}
	for _, handle := range handles {
		results = append(results, r.finishExplorationSession(ctx, run, redactor, handle, controllerErr))
	}
	results = append(results, failedResults...)
	return results
}

func (r *Runner) startExplorationSession(
	ctx context.Context,
	run *Run,
	redactor *Redactor,
	spec RunSpec,
	agent AgentSpec,
	target Target,
	runRoot string,
	taskDir string,
	skillsDir string,
) (*explorationSession, error) {
	sessionID := agent.ID
	workspaceDir := sessionWorkspace(runRoot, sessionID)
	if err := os.MkdirAll(workspaceDir, 0o700); err != nil {
		return nil, fmt.Errorf("create exploration session workspace: %w", err)
	}
	factory := agent.Factory
	if factory == nil {
		factory = r.defaultFactory
	}
	if factory == nil {
		return nil, fmt.Errorf("no session factory configured for %s", sessionID)
	}
	env := buildEnvironment(run.ID, sessionID, spec, agent, target, workspaceDir, taskDir, skillsDir)
	session, err := factory.NewSession(ctx, SessionRequest{
		RunID: run.ID, SessionID: sessionID, Agent: agent, Task: spec.Task, Target: target,
		WorkspaceDir: workspaceDir, TaskDir: taskDir, SkillsDir: skillsDir, Env: env, Starter: r.starter,
	})
	if err != nil {
		return nil, err
	}
	run.addSession(sessionID, session)
	r.emitEvent(run, redactor, Event{
		RunID: run.ID, SessionID: sessionID, Source: "harness", Type: "session.started", Time: time.Now().UTC(),
	})
	handle := &explorationSession{
		id: sessionID, agent: agent, session: session, logDone: make(chan struct{}), planner: sessionID == "planner",
	}
	if source, ok := session.(LogSource); ok {
		go func() {
			defer close(handle.logDone)
			for {
				select {
				case log, ok := <-source.Logs():
					if !ok {
						return
					}
					r.emitLog(run, redactor, LogRecord{
						RunID: run.ID, SessionID: sessionID, Stream: log.Stream,
						Time: time.Now().UTC(), Text: log.Text,
					})
				case <-ctx.Done():
					return
				}
			}
		}()
	} else {
		close(handle.logDone)
	}
	return handle, nil
}

func (r *Runner) sessionEventSink(run *Run, redactor *Redactor, handle *explorationSession, budget Budget) EventSink {
	return func(event AgentEvent) {
		r.emitEvent(run, redactor, Event{
			RunID: run.ID, SessionID: handle.id, Source: "agent",
			Type: event.Type, Time: time.Now().UTC(), Data: event.Data,
		})
		r.checkLiveBudget(run, budget, handle.session)
	}
}

func (r *Runner) finishExplorationSession(ctx context.Context, run *Run, redactor *Redactor, handle *explorationSession, controllerErr error) SessionResult {
	_ = handle.session.Abort(context.Background())
	_ = handle.session.Close(context.Background())
	select {
	case <-handle.logDone:
	case <-time.After(2 * time.Second):
	}
	result := SessionResult{ID: handle.id, Status: StatusSucceeded}
	if ctx.Err() != nil {
		result.Status = StatusCancelled
		result.Error = ctx.Err().Error()
	} else if controllerErr != nil && !handle.planner {
		result.Status = StatusFailed
		result.Error = redactor.Redact(controllerErr.Error())
	}
	if source, ok := handle.session.(UsageSource); ok {
		result.Usage = source.Usage()
	}
	r.emitEvent(run, redactor, Event{
		RunID: run.ID, SessionID: handle.id, Source: "harness", Type: "session.finished",
		Time: time.Now().UTC(), Data: mustJSON(result),
	})
	return result
}

func sessionWorkspace(runRoot, sessionID string) string {
	return filepath.Join(runRoot, "sessions", sessionID)
}

func taskTargets(task Task, provisioned Target) []Target {
	targets := append([]Target(nil), task.Targets...)
	if provisioned.ID != "" || len(provisioned.Addresses) > 0 {
		targets = append(targets, provisioned)
	}
	return targets
}

type completionTracker struct {
	mu     sync.RWMutex
	latest Completion
}

func (t *completionTracker) Update(completion Completion) (Completion, bool) {
	if t == nil {
		return Completion{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	changed := t.latest.Progress != completion.Progress ||
		t.latest.Total != completion.Total ||
		t.latest.Done != completion.Done ||
		t.latest.Success != completion.Success
	t.latest = completion
	return completion, changed
}

func (t *completionTracker) Snapshot() Completion {
	if t == nil {
		return Completion{}
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.latest
}

func (r *Runner) recordBackendProgress(run *Run, redactor *Redactor, tracker *completionTracker, completion Completion) {
	if tracker == nil || (completion.Progress == 0 && completion.Total == 0 && !completion.Done) {
		return
	}
	if _, changed := tracker.Update(completion); !changed {
		return
	}
	r.emitEvent(run, redactor, Event{
		RunID: run.ID, Source: "backend", Type: "backend.progress",
		Time: time.Now().UTC(), Data: mustJSON(completion),
	})
}
