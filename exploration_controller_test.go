package harness

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

type sequencePlanner struct {
	mu    sync.Mutex
	plans []PlanOutput
	err   error
	calls int
}

func (p *sequencePlanner) Plan(context.Context, PlanRequest) (PlanOutput, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.err != nil {
		return PlanOutput{}, p.err
	}
	if len(p.plans) == 0 {
		return PlanOutput{}, nil
	}
	plan := p.plans[0]
	p.plans = p.plans[1:]
	return plan, nil
}

type recordingExplorer struct {
	id             string
	reportFn       func(FrontierItem) (ExplorerReport, error)
	resetErr       error
	mu             sync.Mutex
	resets         int
	investigations int
	items          []FrontierItem
	briefs         []string
}

func (e *recordingExplorer) ID() string { return e.id }

func (e *recordingExplorer) Reset(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resets++
	return e.resetErr
}

func (e *recordingExplorer) Investigate(_ context.Context, item FrontierItem, brief string) (ExplorerReport, error) {
	e.mu.Lock()
	e.investigations++
	e.items = append(e.items, item)
	e.briefs = append(e.briefs, brief)
	e.mu.Unlock()
	if e.reportFn == nil {
		return ExplorerReport{ItemID: item.ID, Verdict: VerdictInconclusive}, nil
	}
	return e.reportFn(item)
}

func TestControllerRunsOneFrontierPerExecutorAndResetsContext(t *testing.T) {
	target := Target{ID: "web", Addresses: []string{"10.0.0.5:80"}}
	planner := &sequencePlanner{plans: []PlanOutput{{NextItems: []ExplorerProposal{
		{Question: "Inspect the HTTP root", Target: "10.0.0.5", ExpectedEvidence: "status and headers"},
		{Question: "Inspect the admin path", Target: "10.0.0.5", ExpectedEvidence: "admin response"},
	}}}}
	report := func(item FrontierItem) (ExplorerReport, error) {
		return ExplorerReport{ItemID: item.ID, Verdict: VerdictInconclusive}, nil
	}
	left := &recordingExplorer{id: "left", reportFn: report}
	right := &recordingExplorer{id: "right", reportFn: report}
	controller := NewController(ControllerConfig{
		Rounds: 2, Objective: "find the flag", Targets: []Target{target}, MaxWorkers: 2,
	}, planner, []Explorer{left, right}, nil)
	if err := controller.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if left.investigations+right.investigations != 2 {
		t.Fatalf("investigations = %d, want 2", left.investigations+right.investigations)
	}
	if left.investigations != 1 || right.investigations != 1 {
		t.Fatalf("left=%d right=%d, want one each", left.investigations, right.investigations)
	}
	if left.items[0].ID == right.items[0].ID {
		t.Fatalf("executors received the same frontier: %q", left.items[0].ID)
	}
	if left.resets != 1 || right.resets != 1 {
		t.Fatalf("resets left=%d right=%d", left.resets, right.resets)
	}
	if !strings.Contains(left.briefs[0], left.items[0].Question) ||
		!strings.Contains(right.briefs[0], right.items[0].Question) {
		t.Fatal("executor brief did not contain its assigned frontier")
	}
}

func TestControllerFailsMismatchedReportWithoutMergingFacts(t *testing.T) {
	planner := &sequencePlanner{plans: []PlanOutput{{NextItems: []ExplorerProposal{{
		Question: "Inspect the login path", Target: "web", ExpectedEvidence: "login response",
	}}}}}
	explorer := &recordingExplorer{id: "executor", reportFn: func(FrontierItem) (ExplorerReport, error) {
		return ExplorerReport{
			ItemID: "wrong-item", Verdict: VerdictFound,
			Claims: []ExplorerClaim{{Claim: "invented", Evidence: "forged"}},
		}, nil
	}}
	var events []ControllerEvent
	controller := NewController(ControllerConfig{
		Rounds: 1, Objective: "find the flag",
		Targets: []Target{{ID: "web", Addresses: []string{"10.0.0.5:80"}}},
	}, planner, []Explorer{explorer}, func(event ControllerEvent) {
		events = append(events, event)
	})
	if err := controller.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(controller.Facts()) != 0 {
		t.Fatalf("mismatched report polluted ledger: %+v", controller.Facts())
	}
	items := controller.Items()
	if len(items) != 1 || items[0].Status != FrontierFailed {
		t.Fatalf("items = %+v, want one failed frontier", items)
	}
	if !hasControllerEvent(events, "item.failed") {
		t.Fatalf("events = %+v, want item.failed", events)
	}
}

func TestControllerDegradesBrokenPlannerToSeedItems(t *testing.T) {
	planner := &sequencePlanner{err: errors.New("planner unavailable")}
	explorer := &recordingExplorer{id: "executor", reportFn: func(item FrontierItem) (ExplorerReport, error) {
		return ExplorerReport{ItemID: item.ID, Verdict: VerdictInconclusive}, nil
	}}
	var events []ControllerEvent
	controller := NewController(ControllerConfig{
		Rounds: 1, Objective: "find the flag",
		Targets: []Target{{ID: "web", Addresses: []string{"10.0.0.5:80"}}},
	}, planner, []Explorer{explorer}, func(event ControllerEvent) {
		events = append(events, event)
	})
	if err := controller.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if explorer.investigations != 1 {
		t.Fatalf("investigations = %d, want 1", explorer.investigations)
	}
	if !hasControllerEvent(events, "planner.degraded") {
		t.Fatalf("events = %+v, want planner.degraded", events)
	}
	if items := controller.Items(); len(items) != 1 || items[0].Status != FrontierDone {
		t.Fatalf("items = %+v", items)
	}
}

func TestControllerDeduplicatesClosedFrontiers(t *testing.T) {
	proposal := ExplorerProposal{
		Question: "Fetch robots.txt", Target: "web", ExpectedEvidence: "robots response",
	}
	planner := &sequencePlanner{plans: []PlanOutput{
		{NextItems: []ExplorerProposal{proposal}},
		{NextItems: []ExplorerProposal{proposal}},
	}}
	explorer := &recordingExplorer{id: "executor", reportFn: func(item FrontierItem) (ExplorerReport, error) {
		return ExplorerReport{ItemID: item.ID, Verdict: VerdictNotFound}, nil
	}}
	var events []ControllerEvent
	controller := NewController(ControllerConfig{
		Rounds: 3, Objective: "find the flag",
		Targets: []Target{{ID: "web", Addresses: []string{"10.0.0.5:80"}}},
	}, planner, []Explorer{explorer}, func(event ControllerEvent) {
		events = append(events, event)
	})
	if err := controller.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if explorer.investigations != 1 {
		t.Fatalf("investigations = %d, want 1", explorer.investigations)
	}
	if !hasControllerEvent(events, "items.dropped") {
		t.Fatalf("events = %+v, want items.dropped", events)
	}
}

func TestControllerStopsAtRoundLimit(t *testing.T) {
	planner := &roundPlanner{}
	explorer := &recordingExplorer{id: "executor", reportFn: func(item FrontierItem) (ExplorerReport, error) {
		return ExplorerReport{ItemID: item.ID, Verdict: VerdictInconclusive}, nil
	}}
	controller := NewController(ControllerConfig{
		Rounds: 2, Objective: "find the flag",
		Targets: []Target{{ID: "web", Addresses: []string{"10.0.0.5:80"}}},
	}, planner, []Explorer{explorer}, nil)
	if err := controller.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if explorer.investigations != 2 {
		t.Fatalf("investigations = %d, want 2", explorer.investigations)
	}
}

func TestControllerDoesNotEmitSuccessFromReports(t *testing.T) {
	planner := &sequencePlanner{plans: []PlanOutput{{NextItems: []ExplorerProposal{{
		Question: "Claim completion", Target: "web", ExpectedEvidence: "agent statement",
	}}}}}
	explorer := &recordingExplorer{id: "executor", reportFn: func(item FrontierItem) (ExplorerReport, error) {
		return ExplorerReport{ItemID: item.ID, Verdict: VerdictFound}, nil
	}}
	var events []ControllerEvent
	controller := NewController(ControllerConfig{
		Rounds: 1, Objective: "find the flag",
		Targets: []Target{{ID: "web", Addresses: []string{"10.0.0.5:80"}}},
	}, planner, []Explorer{explorer}, func(event ControllerEvent) {
		events = append(events, event)
	})
	if err := controller.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, event := range events {
		if strings.Contains(strings.ToLower(event.Type), "success") {
			t.Fatalf("controller emitted success event: %+v", events)
		}
	}
}

func TestControllerFailsResetWithoutInvestigation(t *testing.T) {
	planner := &sequencePlanner{plans: []PlanOutput{{NextItems: []ExplorerProposal{{
		Question: "Inspect root", Target: "web", ExpectedEvidence: "HTTP response",
	}}}}}
	explorer := &recordingExplorer{id: "executor", resetErr: errors.New("reset failed")}
	controller := NewController(ControllerConfig{
		Rounds: 1, Objective: "find the flag",
		Targets: []Target{{ID: "web", Addresses: []string{"10.0.0.5:80"}}},
	}, planner, []Explorer{explorer}, nil)
	if err := controller.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if explorer.investigations != 0 {
		t.Fatalf("investigations = %d, want 0", explorer.investigations)
	}
	items := controller.Items()
	if len(items) != 1 || items[0].Status != FrontierFailed {
		t.Fatalf("items = %+v", items)
	}
}

func TestControllerPublishesInitializationBeforeDispatch(t *testing.T) {
	target := Target{ID: "web", Addresses: []string{"10.0.0.5:80"}}
	planner := &sequencePlanner{plans: []PlanOutput{{NextItems: []ExplorerProposal{{
		Question: "Inspect the HTTP root", Target: "web", ExpectedEvidence: "status and headers",
	}}}}}
	explorer := &recordingExplorer{id: "executor", reportFn: func(item FrontierItem) (ExplorerReport, error) {
		return ExplorerReport{ItemID: item.ID, Verdict: VerdictInconclusive}, nil
	}}
	recorder := &controllerEventRecorder{}
	controller := NewController(ControllerConfig{
		Rounds: 1, Objective: "find the flag", Targets: []Target{target},
		Progress: func() Completion { return Completion{Progress: 0, Total: 1} },
	}, planner, []Explorer{explorer}, recorder.emit)
	if err := controller.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := recorder.snapshot()
	initialized := eventIndex(events, "initialized")
	created := eventIndex(events, "frontiers.created")
	ready := eventIndex(events, "batch.ready")
	assigned := eventIndex(events, "item.assigned")
	started := eventIndex(events, "executor.started")
	if initialized < 0 || created < initialized || ready < created || assigned < ready || started < assigned {
		t.Fatalf("initialization order invalid: %+v", events)
	}
	initialData := events[initialized].Data
	if initialData["phase"] != "initialized" || initialData["objective"] != "find the flag" {
		t.Fatalf("initial data = %+v", initialData)
	}
	createdItems, ok := events[created].Data["items"].([]FrontierItem)
	if !ok || len(createdItems) != 1 || createdItems[0].Question != "Inspect the HTTP root" {
		t.Fatalf("created data = %+v", events[created].Data)
	}
	assignedItem, ok := events[assigned].Data["item"].(FrontierItem)
	if !ok || assignedItem.Question != "Inspect the HTTP root" {
		t.Fatalf("assigned data = %+v", events[assigned].Data)
	}
}

func TestControllerAcquiresExecutorOnlyAfterFrontiersReady(t *testing.T) {
	target := Target{ID: "web", Addresses: []string{"10.0.0.5:80"}}
	planner := &sequencePlanner{plans: []PlanOutput{{NextItems: []ExplorerProposal{{
		Question: "Inspect the HTTP root", Target: "web", ExpectedEvidence: "status and headers",
	}}}}}
	recorder := &controllerEventRecorder{}
	acquired := 0
	explorer := &recordingExplorer{id: "executor", reportFn: func(item FrontierItem) (ExplorerReport, error) {
		return ExplorerReport{ItemID: item.ID, Verdict: VerdictInconclusive}, nil
	}}
	controller := NewController(ControllerConfig{
		Rounds: 1, Objective: "find the flag", Targets: []Target{target},
		AcquireExplorers: func(context.Context) ([]Explorer, error) {
			acquired++
			events := recorder.snapshot()
			if eventIndex(events, "frontiers.created") < 0 {
				t.Fatal("executor acquired before frontiers were published")
			}
			if eventIndex(events, "item.assigned") >= 0 {
				t.Fatal("executor acquired after assignment")
			}
			return []Explorer{explorer}, nil
		},
	}, planner, nil, recorder.emit)
	if err := controller.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if acquired != 1 || explorer.investigations != 1 {
		t.Fatalf("acquired=%d investigations=%d", acquired, explorer.investigations)
	}
}

type roundPlanner struct {
	mu sync.Mutex
}

func (p *roundPlanner) Plan(_ context.Context, req PlanRequest) (PlanOutput, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return PlanOutput{NextItems: []ExplorerProposal{{
		Question:         fmt.Sprintf("Inspect round %d", req.Round),
		Target:           "web",
		ExpectedEvidence: "new evidence",
	}}}, nil
}

func hasControllerEvent(events []ControllerEvent, want string) bool {
	for _, event := range events {
		if event.Type == want {
			return true
		}
	}
	return false
}

type controllerEventRecorder struct {
	mu     sync.Mutex
	events []ControllerEvent
}

func (r *controllerEventRecorder) emit(event ControllerEvent) {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
}

func (r *controllerEventRecorder) snapshot() []ControllerEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ControllerEvent(nil), r.events...)
}

func eventIndex(events []ControllerEvent, eventType string) int {
	for index, event := range events {
		if event.Type == eventType {
			return index
		}
	}
	return -1
}
