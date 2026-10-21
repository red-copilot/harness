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
		{
			Question: "Is the HTTP root served?", Target: "10.0.0.5", ProbeKey: "http.get",
			Params: []string{"/"}, ExpectedEvidence: "HTTP status and headers", FailureEvidence: "connection failure",
		},
		{
			Question: "Is an admin page present?", Target: "10.0.0.5", ProbeKey: "http.get",
			Params: []string{"/admin"}, ExpectedEvidence: "admin response", FailureEvidence: "HTTP 404",
		},
	}}}}
	report := func(item FrontierItem) (ExplorerReport, error) {
		return ExplorerReport{ItemID: item.ID, Verdict: VerdictInconclusive}, nil
	}
	left := &recordingExplorer{id: "left", reportFn: report}
	right := &recordingExplorer{id: "right", reportFn: report}
	controller := NewController(ControllerConfig{
		Rounds: 1, Objective: "find the flag", Targets: []Target{target}, MaxWorkers: 2,
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

func TestControllerDefaultsToThreeConcurrentFrontiers(t *testing.T) {
	target := Target{ID: "web", Addresses: []string{"10.0.0.5:80"}}
	planner := &sequencePlanner{plans: []PlanOutput{{NextItems: []ExplorerProposal{
		{
			Question: "Is the HTTP root served?", Target: "10.0.0.5", ProbeKey: "http.get",
			Params: []string{"/"}, ExpectedEvidence: "HTTP status and headers", FailureEvidence: "connection failure",
		},
		{
			Question: "Is an admin page present?", Target: "10.0.0.5", ProbeKey: "http.get",
			Params: []string{"/admin"}, ExpectedEvidence: "admin response", FailureEvidence: "HTTP 404",
		},
	}}}}
	report := func(item FrontierItem) (ExplorerReport, error) {
		return ExplorerReport{ItemID: item.ID, Verdict: VerdictInconclusive}, nil
	}
	executors := []*recordingExplorer{
		{id: "left", reportFn: report},
		{id: "middle", reportFn: report},
		{id: "right", reportFn: report},
	}
	explorers := []Explorer{executors[0], executors[1], executors[2]}
	controller := NewController(ControllerConfig{
		Rounds: 1, Objective: "find the flag", Targets: []Target{target},
	}, planner, explorers, nil)
	if err := controller.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, executor := range executors {
		if executor.investigations != 1 {
			t.Fatalf("%s investigations = %d, want 1", executor.id, executor.investigations)
		}
	}
}

func TestControllerAppliesFrontierBudgetsBeforeDispatch(t *testing.T) {
	target := Target{ID: "web", Addresses: []string{"10.0.0.5:80"}}
	planner := &sequencePlanner{plans: []PlanOutput{{NextItems: []ExplorerProposal{{
		Question: "Does the HTTP root expose an app?", Target: "web",
		ProbeKey: "http.get", Params: []string{"/"},
		ExpectedEvidence: "HTTP 200 with HTML", FailureEvidence: "HTTP 404 or non-HTML content",
	}}}}}
	explorer := &recordingExplorer{id: "executor", reportFn: func(item FrontierItem) (ExplorerReport, error) {
		return ExplorerReport{ItemID: item.ID, Verdict: VerdictInconclusive}, nil
	}}
	controller := NewController(ControllerConfig{
		Rounds: 1, Objective: "find the flag", Targets: []Target{target},
	}, planner, []Explorer{explorer}, nil)
	if err := controller.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	items := controller.Items()
	if len(items) != 2 {
		t.Fatalf("items = %d, want the planner frontier plus the brief-derived seed", len(items))
	}
	for _, item := range items {
		if item.MaxTools != 8 || item.MaxDurationSeconds != 90 {
			t.Fatalf("budget = tools %d duration %ds, want 8 tools / 90s",
				item.MaxTools, item.MaxDurationSeconds)
		}
	}
	dispatched := items[1]
	if dispatched.ProbeKey != "http.get" || dispatched.Status != FrontierDone {
		t.Fatalf("planner frontier = %+v, want a done http.get experiment", dispatched)
	}
}

func TestControllerFailsMismatchedReportWithoutMergingFacts(t *testing.T) {
	planner := &sequencePlanner{plans: []PlanOutput{{NextItems: []ExplorerProposal{{
		Question: "Does /login expose a form?", Target: "web", ProbeKey: "http.get", Params: []string{"/login"},
		ExpectedEvidence: "login form", FailureEvidence: "HTTP 404",
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
	failed := 0
	for _, item := range controller.Items() {
		if item.Status == FrontierFailed {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("items = %+v, want one failed frontier", controller.Items())
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
		Question: "Are crawler directives published?", Target: "web", ProbeKey: "http.get",
		Params: []string{"/robots.txt"}, ExpectedEvidence: "robots response", FailureEvidence: "HTTP 404",
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
	// The planner frontier runs first, then the brief-derived seed frontier.
	if explorer.investigations != 2 {
		t.Fatalf("investigations = %d, want 2", explorer.investigations)
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
		Question: "Does the root return an HTTP response?", Target: "web", ProbeKey: "http.get",
		Params: []string{"/"}, ExpectedEvidence: "HTTP response", FailureEvidence: "connection failure",
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
		Question: "Does the root return an HTTP response?", Target: "web", ProbeKey: "http.get",
		Params: []string{"/"}, ExpectedEvidence: "HTTP response", FailureEvidence: "connection failure",
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
	failed := 0
	for _, item := range controller.Items() {
		if item.Status == FrontierFailed {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("items = %+v, want one failed frontier", controller.Items())
	}
}

func TestControllerPublishesInitializationBeforeDispatch(t *testing.T) {
	target := Target{ID: "web", Addresses: []string{"10.0.0.5:80"}}
	planner := &sequencePlanner{plans: []PlanOutput{{NextItems: []ExplorerProposal{{
		Question: "Is the HTTP root served?", Target: "web", ProbeKey: "http.get",
		Params: []string{"/"}, ExpectedEvidence: "status and headers", FailureEvidence: "connection failure",
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
	if !ok || len(createdItems) != 1 || createdItems[0].Question != "Is the HTTP root served?" {
		t.Fatalf("created data = %+v", events[created].Data)
	}
	assignedItem, ok := events[assigned].Data["item"].(FrontierItem)
	if !ok || assignedItem.Question != "Is the HTTP root served?" {
		t.Fatalf("assigned data = %+v", events[assigned].Data)
	}
}

func TestControllerAcquiresExecutorOnlyAfterFrontiersReady(t *testing.T) {
	target := Target{ID: "web", Addresses: []string{"10.0.0.5:80"}}
	planner := &sequencePlanner{plans: []PlanOutput{{NextItems: []ExplorerProposal{{
		Question: "Is the HTTP root served?", Target: "web", ProbeKey: "http.get",
		Params: []string{"/"}, ExpectedEvidence: "status and headers", FailureEvidence: "connection failure",
	}}}}}
	recorder := &controllerEventRecorder{}
	acquired := 0
	explorer := &recordingExplorer{id: "executor", reportFn: func(item FrontierItem) (ExplorerReport, error) {
		return ExplorerReport{ItemID: item.ID, Verdict: VerdictInconclusive}, nil
	}}
	controller := NewController(ControllerConfig{
		Rounds: 1, Objective: "find the flag", Targets: []Target{target},
		AcquireExplorers: func(_ context.Context, need int) ([]Explorer, error) {
			acquired++
			// One planner frontier plus the brief-derived seed frontier.
			if need != 2 {
				t.Fatalf("acquire need = %d, want the ready frontier count 2", need)
			}
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
		Question:         fmt.Sprintf("Does round %d path exist?", req.Round),
		Target:           "web",
		ProbeKey:         "http.get",
		Params:           []string{fmt.Sprintf("/round-%d", req.Round)},
		ExpectedEvidence: "new evidence",
		FailureEvidence:  "HTTP 404",
	}}}, nil
}

func TestControllerForwardsTaskMetadataToPlanner(t *testing.T) {
	target := Target{ID: "web", Addresses: []string{"10.0.0.5:80"}}
	planner := &metadataPlanner{}
	explorer := &recordingExplorer{id: "executor", reportFn: func(item FrontierItem) (ExplorerReport, error) {
		return ExplorerReport{ItemID: item.ID, Verdict: VerdictInconclusive}, nil
	}}
	controller := NewController(ControllerConfig{
		Rounds: 1, Objective: "find the flag", Targets: []Target{target},
		Metadata: map[string]string{"challenge_code": "c-07", "challenge_description": "Telnet 服务"},
	}, planner, []Explorer{explorer}, nil)
	if err := controller.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if planner.metadata["challenge_description"] != "Telnet 服务" {
		t.Fatalf("planner metadata = %v, want the task metadata", planner.metadata)
	}
}

type metadataPlanner struct {
	metadata map[string]string
}

func (p *metadataPlanner) Plan(_ context.Context, req PlanRequest) (PlanOutput, error) {
	p.metadata = req.Metadata
	return PlanOutput{NextItems: []ExplorerProposal{{
		Question: "服务是否返回横幅？", Target: "web", ProbeKey: "service.banner",
		Params: []string{"10.0.0.5:80"}, ExpectedEvidence: "横幅", FailureEvidence: "连接被拒绝",
	}}}, nil
}

func TestControllerInitializesBlackboardFromTaskBrief(t *testing.T) {
	recorder := &controllerEventRecorder{}
	planner := &sequencePlanner{}
	controller := NewController(ControllerConfig{
		Rounds: 1, Objective: "找出全部 flag",
		Metadata: map[string]string{"challenge_description": "对目标远程登录服务进行安全测试。"},
		Targets:  []Target{{ID: "c-07", Addresses: []string{"10.0.169.88:23"}}},
	}, planner, []Explorer{&recordingExplorer{id: "executor", reportFn: func(item FrontierItem) (ExplorerReport, error) {
		return ExplorerReport{ItemID: item.ID, Verdict: VerdictInconclusive}, nil
	}}}, recorder.emit)
	if err := controller.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := recorder.snapshot()
	index := eventIndex(events, "initialized")
	if index < 0 {
		t.Fatal("no initialized event")
	}
	initial := events[index].Data
	brief, ok := initial["brief"].(map[string]string)
	if !ok || brief["challenge_description"] != "对目标远程登录服务进行安全测试。" {
		t.Fatalf("initialized brief = %#v", initial["brief"])
	}
	seeds, ok := initial["frontiers"].([]FrontierItem)
	if !ok || len(seeds) != 1 {
		t.Fatalf("initialized frontiers = %#v, want one brief-derived seed", initial["frontiers"])
	}
	if !strings.Contains(seeds[0].Question, "远程登录服务") {
		t.Fatalf("seed question does not quote the brief: %q", seeds[0].Question)
	}
	if seeds[0].ProbeKey != "service.banner" || seeds[0].Status != FrontierOpen {
		t.Fatalf("seed item = %+v", seeds[0])
	}
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

func TestControllerPublishesFactContentWhenMerging(t *testing.T) {
	target := Target{ID: "web", Addresses: []string{"10.0.0.5:23"}}
	planner := &sequencePlanner{plans: []PlanOutput{{NextItems: []ExplorerProposal{{
		Question: "端口 23 是否为 HTTP 服务？", Target: "web", ProbeKey: "http.get",
		Params: []string{"http://10.0.0.5:23/"}, ExpectedEvidence: "HTTP 状态行",
		FailureEvidence: "非 HTTP 响应",
	}}}}}
	explorer := &recordingExplorer{id: "executor", reportFn: func(item FrontierItem) (ExplorerReport, error) {
		return ExplorerReport{ItemID: item.ID, Verdict: VerdictRefuted, Claims: []ExplorerClaim{{
			Claim:    "端口 23 不是 HTTP 服务，而是 Telnet",
			Evidence: "curl 超时并返回 15 字节原始数据",
			Targets:  []string{"web"},
		}}}, nil
	}}
	recorder := &controllerEventRecorder{}
	controller := NewController(ControllerConfig{
		Rounds: 1, Objective: "找出 flag", Targets: []Target{target},
	}, planner, []Explorer{explorer}, recorder.emit)
	if err := controller.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	index := eventIndex(recorder.snapshot(), "facts.merged")
	if index < 0 {
		t.Fatal("no facts.merged event")
	}
	facts, ok := recorder.snapshot()[index].Data["facts"].([]Fact)
	if !ok || len(facts) != 1 {
		t.Fatalf("facts payload = %#v, want one fact", recorder.snapshot()[index].Data["facts"])
	}
	if facts[0].ID == "" || facts[0].Claim != "端口 23 不是 HTTP 服务，而是 Telnet" {
		t.Fatalf("fact payload = %+v, want id and claim text", facts[0])
	}
	if facts[0].Evidence == "" || len(facts[0].Targets) == 0 {
		t.Fatalf("fact payload lost evidence/targets: %+v", facts[0])
	}
}
