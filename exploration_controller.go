package harness

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Explorer executes exactly one frontier in a fresh context.
type Explorer interface {
	ID() string
	Reset(context.Context) error
	Investigate(context.Context, FrontierItem, string) (ExplorerReport, error)
}

// ControllerEvent is a bounded, structured exploration-state transition.
type ControllerEvent struct {
	Type string
	Data map[string]any
}

// ControllerConfig defines one run-scoped blackboard.
type ControllerConfig struct {
	Rounds                  int
	Objective               string
	Metadata                map[string]string
	Targets                 []Target
	MaxWorkers              int
	MaxToolsPerFrontier     int
	FrontierDurationSeconds int
	Progress                func() Completion
	// AcquireExplorers lazily starts exactly as many fresh-context Executors as
	// the current batch has ready frontiers, bounded by MaxWorkers.
	AcquireExplorers func(context.Context, int) ([]Explorer, error)
}

// Controller is the deterministic state machine. It never establishes success.
type Controller struct {
	cfg       ControllerConfig
	planner   Planner
	explorers []Explorer
	emit      func(ControllerEvent)

	ledger  *FactLedger
	items   []FrontierItem
	reports []ExplorerReport
}

func NewController(cfg ControllerConfig, planner Planner, explorers []Explorer, emit func(ControllerEvent)) *Controller {
	if cfg.Rounds <= 0 {
		cfg.Rounds = 6
	}
	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = DefaultExplorationWorkers
	}
	if cfg.MaxToolsPerFrontier <= 0 {
		cfg.MaxToolsPerFrontier = DefaultFrontierToolLimit
	}
	if cfg.MaxToolsPerFrontier > MaxFrontierToolLimit {
		cfg.MaxToolsPerFrontier = MaxFrontierToolLimit
	}
	if cfg.FrontierDurationSeconds <= 0 {
		cfg.FrontierDurationSeconds = DefaultFrontierDurationSeconds
	}
	if cfg.FrontierDurationSeconds > MaxFrontierDurationSeconds {
		cfg.FrontierDurationSeconds = MaxFrontierDurationSeconds
	}
	return &Controller{
		cfg: cfg, planner: planner, explorers: append([]Explorer(nil), explorers...),
		emit: emit, ledger: NewFactLedger(),
	}
}

// Run advances the blackboard for at most cfg.Rounds. It returns nil when the
// frontier space is exhausted; operational failures are returned separately so
// the Runner can fail closed.
func (c *Controller) Run(ctx context.Context) error {
	if len(c.explorers) == 0 && c.cfg.AcquireExplorers == nil {
		return errors.New("exploration controller requires at least one executor")
	}
	c.emitInitialized()
	for round := 0; round < c.cfg.Rounds; round++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		// The blackboard is seeded from the task brief at initialization, so
		// round 0 still asks the Planner for sharper experiments.
		if round == 0 || len(c.openIndexes()) == 0 {
			c.plan(ctx, round)
		}
		indexes := c.openIndexes()
		if len(indexes) == 0 {
			return nil
		}
		workers := c.cfg.MaxWorkers
		if workers > len(indexes) {
			workers = len(indexes)
		}
		// One Executor per ready frontier: the batch size drives how many
		// sessions are started, never the other way round.
		if workers > len(c.explorers) && c.cfg.AcquireExplorers != nil {
			explorers, err := c.cfg.AcquireExplorers(ctx, workers)
			if err != nil {
				return fmt.Errorf("acquire explorers: %w", err)
			}
			c.explorers = append([]Explorer(nil), explorers...)
		}
		if workers > len(c.explorers) {
			workers = len(c.explorers)
		}
		if workers == 0 {
			return errors.New("no executors available after initialization")
		}
		batch := make([]FrontierItem, 0, workers)
		for _, index := range indexes[:workers] {
			batch = append(batch, copyFrontierItem(c.items[index]))
		}
		c.emitEvent("batch.ready", map[string]any{
			"round": round + 1, "batch": batch, "max_workers": workers,
		})
		if err := c.runRound(ctx, round, indexes[:workers]); err != nil {
			return err
		}
	}
	return nil
}

// Facts returns a copy of the current ledger in insertion order.
func (c *Controller) Facts() []Fact {
	return c.ledger.Facts()
}

// Items returns all frontiers, including closed fingerprints used for dedup.
func (c *Controller) Items() []FrontierItem {
	out := make([]FrontierItem, len(c.items))
	for i, item := range c.items {
		out[i] = copyFrontierItem(item)
	}
	return out
}

func (c *Controller) plan(ctx context.Context, round int) {
	c.emitEvent("phase.changed", map[string]any{"phase": "planning", "round": round + 1})
	request := PlanRequest{
		Round: round, Objective: c.cfg.Objective, Metadata: c.cfg.Metadata,
		Targets: append([]Target(nil), c.cfg.Targets...),
		Facts: plannerFactDigest(c.ledger.Facts(), MaxDigestFacts), OpenItems: c.itemsAtStatus(FrontierOpen),
		RecentItems: c.Items(), Reports: append([]ExplorerReport(nil), c.reports...),
	}
	if c.cfg.Progress != nil {
		request.Progress = c.cfg.Progress()
	}

	var output PlanOutput
	var err error
	if c.planner == nil {
		err = errors.New("planner is not configured")
	} else {
		output, err = c.planner.Plan(ctx, request)
	}
	if err != nil {
		c.emitEvent("planner.degraded", map[string]any{"round": round, "error": err.Error()})
		c.addSeeds(round)
		return
	}
	validation, validateErr := ValidatePlan(output, c.ledger, c.items, c.cfg.Targets)
	if validateErr != nil {
		c.emitEvent("planner.degraded", map[string]any{"round": round, "error": validateErr.Error()})
		c.addSeeds(round)
		return
	}
	c.addValidated(validation, round, "")
}

func (c *Controller) addSeeds(round int) {
	validation := PlanValidation{}
	seen := c.fingerprints()
	for _, item := range SeedItems(c.cfg.Targets, c.cfg.Objective, c.cfg.Metadata, round+1) {
		if _, ok := seen[item.Fingerprint]; ok {
			validation.Dropped = append(validation.Dropped, DroppedItem{
				Proposal: ExplorerProposal{
					Question: item.Question, Target: item.TargetHint, ExpectedEvidence: item.ExpectedEvidence,
				},
				Reason: "duplicate frontier",
			})
			continue
		}
		seen[item.Fingerprint] = struct{}{}
		validation.Accepted = append(validation.Accepted, item)
	}
	c.addValidated(validation, round, "")
}

func (c *Controller) addValidated(validation PlanValidation, round int, parentID string) {
	created := make([]FrontierItem, 0, len(validation.Accepted))
	for _, item := range validation.Accepted {
		item.Round = round + 1
		if item.MaxTools <= 0 {
			item.MaxTools = c.cfg.MaxToolsPerFrontier
		}
		if item.MaxDurationSeconds <= 0 {
			item.MaxDurationSeconds = c.cfg.FrontierDurationSeconds
		}
		if item.Fingerprint == "" {
			item.Fingerprint = FrontierFingerprint(item)
		}
		if item.ID == "" {
			item.ID = "frontier-" + item.Fingerprint[:16]
		}
		item.ParentID = parentID
		item.Status = FrontierOpen
		c.items = append(c.items, item)
		created = append(created, copyFrontierItem(item))
	}
	c.emitEvent("frontiers.created", map[string]any{"round": round + 1, "items": created})
	c.emitEvent("items.planned", map[string]any{
		"round": round + 1, "accepted": len(validation.Accepted), "dropped": len(validation.Dropped),
	})
	if len(validation.Dropped) > 0 {
		dropped := make([]map[string]string, 0, len(validation.Dropped))
		for _, item := range validation.Dropped {
			dropped = append(dropped, map[string]string{
				"question": item.Proposal.Question, "reason": item.Reason,
			})
		}
		c.emitEvent("items.dropped", map[string]any{"round": round + 1, "items": dropped})
	}
	if len(c.itemsAtStatus(FrontierOpen)) > 0 {
		c.emitEvent("phase.changed", map[string]any{"phase": "ready", "round": round + 1})
	}
}

func (c *Controller) runRound(ctx context.Context, round int, indexes []int) error {
	type result struct {
		index    int
		explorer Explorer
		report   ExplorerReport
		err      error
	}

	c.emitEvent("round.started", map[string]any{"round": round + 1, "batch_size": len(indexes)})
	c.emitEvent("phase.changed", map[string]any{"phase": "dispatching", "round": round + 1})
	results := make(chan result, len(indexes))
	var wg sync.WaitGroup
	for position, index := range indexes {
		explorer := c.explorers[position]
		c.items[index].Status = FrontierAssigned
		c.items[index].AssignedTo = explorer.ID()
		item := copyFrontierItem(c.items[index])
		c.emitEvent("item.assigned", map[string]any{
			"round": round + 1, "item_id": item.ID, "executor_id": explorer.ID(), "item": item,
		})
		brief := BuildExplorerBrief(c.cfg.Objective, c.cfg.Targets, item, DigestFacts(item, c.ledger.Facts()))
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := explorer.Reset(ctx); err != nil {
				results <- result{index: index, explorer: explorer, err: fmt.Errorf("reset executor: %w", err)}
				return
			}
			c.emitEvent("executor.started", map[string]any{
				"round": round + 1, "item_id": item.ID, "executor_id": explorer.ID(), "fresh_context": true,
			})
			c.emitEvent("phase.changed", map[string]any{"phase": "executing", "round": round + 1})
			report, err := explorer.Investigate(ctx, item, brief)
			results <- result{index: index, explorer: explorer, report: report, err: err}
		}()
	}
	wg.Wait()
	close(results)

	for outcome := range results {
		item := &c.items[outcome.index]
		if outcome.err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			item.Status = FrontierFailed
			c.emitEvent("item.failed", map[string]any{
				"round": round + 1, "item_id": item.ID, "error": outcome.err.Error(),
			})
			continue
		}
		c.emitEvent("report.received", map[string]any{
			"round": round + 1, "item_id": item.ID, "verdict": outcome.report.Verdict,
		})
		if outcome.report.ItemID != item.ID {
			item.Status = FrontierFailed
			c.emitEvent("item.failed", map[string]any{
				"round": round + 1, "item_id": item.ID,
				"error": fmt.Sprintf("report item_id %q does not match", outcome.report.ItemID),
			})
			continue
		}
		if !validExplorerVerdict(outcome.report.Verdict) {
			item.Status = FrontierFailed
			c.emitEvent("item.failed", map[string]any{
				"round": round + 1, "item_id": item.ID,
				"error": fmt.Sprintf("invalid verdict %q", outcome.report.Verdict),
			})
			continue
		}

		merged := c.ledger.MergeClaims(round+1, item.ID, outcome.explorer.ID(), outcome.report.Claims)
		item.Status = FrontierDone
		c.reports = append(c.reports, outcome.report)
		if len(c.reports) > 32 {
			c.reports = append([]ExplorerReport(nil), c.reports[len(c.reports)-32:]...)
		}
		c.emitEvent("facts.merged", map[string]any{
			"round": round + 1, "item_id": item.ID, "added": merged.Added,
			"corroborated": merged.Corroborated, "contested": merged.Contested,
			"facts": c.ledger.FactsByID(merged.Added, merged.Corroborated),
		})
		if len(outcome.report.Next) > 0 {
			validation, _ := ValidatePlan(PlanOutput{NextItems: outcome.report.Next}, c.ledger, c.items, c.cfg.Targets)
			c.addValidated(validation, round+1, item.ID)
		}
	}
	c.emitEvent("round.completed", map[string]any{"round": round + 1})
	c.emitEvent("phase.changed", map[string]any{"phase": "planning", "round": round + 1})
	return nil
}

func (c *Controller) openIndexes() []int {
	var indexes []int
	for index, item := range c.items {
		if item.Status == FrontierOpen {
			indexes = append(indexes, index)
		}
	}
	sort.SliceStable(indexes, func(i, j int) bool {
		left := c.items[indexes[i]]
		right := c.items[indexes[j]]
		if left.Round != right.Round {
			return left.Round < right.Round
		}
		return left.ID < right.ID
	})
	return indexes
}

func (c *Controller) itemsAtStatus(status FrontierStatus) []FrontierItem {
	out := make([]FrontierItem, 0)
	for _, item := range c.items {
		if item.Status == status {
			out = append(out, copyFrontierItem(item))
		}
	}
	return out
}

func (c *Controller) fingerprints() map[string]struct{} {
	out := make(map[string]struct{}, len(c.items))
	for _, item := range c.items {
		fingerprint := item.Fingerprint
		if fingerprint == "" {
			fingerprint = FrontierFingerprint(item)
		}
		out[fingerprint] = struct{}{}
	}
	return out
}

func (c *Controller) emitEvent(eventType string, data map[string]any) {
	if c.emit != nil {
		c.emit(ControllerEvent{Type: eventType, Data: data})
	}
}

// seed registers the brief-derived frontiers so the board has content from the
// first event onwards and every authorized target gets one identification probe.
func (c *Controller) seed() []FrontierItem {
	seen := c.fingerprints()
	created := make([]FrontierItem, 0, len(c.cfg.Targets))
	for _, item := range SeedItems(c.cfg.Targets, c.cfg.Objective, c.cfg.Metadata, 1) {
		if _, ok := seen[item.Fingerprint]; ok {
			continue
		}
		seen[item.Fingerprint] = struct{}{}
		c.items = append(c.items, item)
		created = append(created, copyFrontierItem(item))
	}
	return created
}

func (c *Controller) emitInitialized() {
	progress := Completion{}
	if c.cfg.Progress != nil {
		progress = c.cfg.Progress()
	}
	seeds := c.seed()
	c.emitEvent("initialized", map[string]any{
		"phase": "initialized", "objective": c.cfg.Objective,
		"brief":    c.cfg.Metadata,
		"targets":  append([]Target(nil), c.cfg.Targets...),
		"progress": progress, "facts": []Fact{}, "frontiers": seeds,
		"policy": map[string]any{
			"rounds": c.cfg.Rounds, "max_workers": c.cfg.MaxWorkers,
			"max_tools_per_frontier":    c.cfg.MaxToolsPerFrontier,
			"frontier_duration_seconds": c.cfg.FrontierDurationSeconds,
			"planner_tool_limit":        DefaultPlannerToolLimit,
			"planner_timeout_seconds":   int(DefaultPlannerTimeout.Seconds()),
		},
	})
}

func validExplorerVerdict(verdict string) bool {
	switch verdict {
	case VerdictSupported, VerdictRefuted, VerdictBlocked, VerdictInconclusive,
		VerdictFound, VerdictNotFound:
		return true
	default:
		return false
	}
}

func copyFrontierItem(item FrontierItem) FrontierItem {
	item.Params = append([]string(nil), item.Params...)
	item.Anchors = append([]string(nil), item.Anchors...)
	item.ReviewOf = append([]string(nil), item.ReviewOf...)
	return item
}
