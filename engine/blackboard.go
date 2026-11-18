package engine

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrIntentCapacity marks a proposal rejected by the single Executor slot.
var ErrIntentCapacity = errors.New("intent capacity exceeded")

// executorSlots is the fixed number of concurrent Executor slots.
const executorSlots = 1

type boardSnapshot struct {
	Task          Task
	Claims        []Claim
	Intents       []Intent
	Attempts      []IntentAttempt
	Observations  []Observation
	Artifacts     []Artifact
	BoardRevision uint64
}

// boardState is the controller-owned, single-writer Claim/Intent blackboard.
type boardState struct {
	runID     string
	task      Task
	target    TargetBinding
	policy    RunPolicy
	claims    []Claim
	intents   []Intent
	attempts  []IntentAttempt
	obs       []Observation
	artifacts []Artifact
	seq       int
	version   uint64
}

func newBoardState(runID string, spec RunSpec, target TargetBinding) *boardState {
	board := &boardState{runID: runID, task: spec.Task, target: target, policy: NormalizeRunPolicy(spec.Policy)}
	board.seq = 2
	originID := "origin"
	goalID := "goal"
	board.claims = append(board.claims,
		Claim{ID: originID, RunID: runID, TaskID: spec.Task.ID, Kind: ClaimOrigin, Description: spec.Task.Objective, CreatedAt: time.Now().UTC()},
		Claim{ID: goalID, RunID: runID, TaskID: spec.Task.ID, Kind: ClaimGoal, Description: spec.Task.Objective, CreatedAt: time.Now().UTC()},
	)
	board.version = 1
	return board
}

func (b *boardState) nextID(kind string) string {
	b.seq++
	return fmt.Sprintf("%s:%s:%d", b.runID, kind, b.seq)
}

func (b *boardState) maxTotal() int {
	if b.policy.Exploration.MaxTotalIntents > 0 {
		return b.policy.Exploration.MaxTotalIntents
	}
	return 20
}

func (b *boardState) snapshot() boardSnapshot {
	return boardSnapshot{
		Task:          b.task,
		Claims:        cloneClaims(b.claims),
		Intents:       cloneIntents(b.intents),
		Attempts:      append([]IntentAttempt(nil), b.attempts...),
		Observations:  cloneObservations(b.obs),
		Artifacts:     append([]Artifact(nil), b.artifacts...),
		BoardRevision: b.version,
	}
}

func (b *boardState) addClaim(claim Claim) (Claim, error) {
	if claim.ID == "" {
		claim.ID = b.nextID("claim")
	}
	if claim.RunID == "" {
		claim.RunID = b.runID
	}
	if claim.TaskID == "" {
		claim.TaskID = b.task.ID
	}
	if claim.CreatedAt.IsZero() {
		claim.CreatedAt = time.Now().UTC()
	}
	if err := ValidateClaim(claim); err != nil {
		return Claim{}, err
	}
	if limit := b.policy.Context.MaxClaimBytes; limit > 0 && len(claim.Description) > limit {
		return Claim{}, fmt.Errorf("claim description exceeds %d bytes", limit)
	}
	if claim.SourceIntentID != "" && b.findIntent(claim.SourceIntentID) == nil {
		return Claim{}, fmt.Errorf("source intent %q does not exist", claim.SourceIntentID)
	}
	b.claims = append(b.claims, claim)
	b.version++
	return claim, nil
}

func (b *boardState) createIntent(intent Intent) (Intent, error) {
	if intent.ID == "" {
		intent.ID = b.nextID("intent")
	}
	if intent.RunID == "" {
		intent.RunID = b.runID
	}
	if intent.State == "" {
		intent.State = IntentOpen
	}
	if intent.CreatedAt.IsZero() {
		intent.CreatedAt = time.Now().UTC()
	}
	if intent.State != IntentOpen {
		return Intent{}, fmt.Errorf("new intent must be open")
	}
	if occupied := b.openCount() + b.claimedCount(); occupied >= executorSlots {
		return Intent{}, fmt.Errorf("%w: %d open/claimed intents", ErrIntentCapacity, occupied)
	}
	if len(b.intents) >= b.maxTotal() {
		return Intent{}, fmt.Errorf("max total intents reached")
	}
	if err := b.validateIntentClaims(intent); err != nil {
		return Intent{}, err
	}
	if err := ValidateIntent(intent); err != nil {
		return Intent{}, err
	}
	if limit := b.policy.Context.MaxIntentBytes; limit > 0 && len(intent.Description) > limit {
		return Intent{}, fmt.Errorf("intent description exceeds %d bytes", limit)
	}
	b.intents = append(b.intents, intent)
	b.version++
	return intent, nil
}

func (b *boardState) validateIntentClaims(intent Intent) error {
	for _, claimID := range intent.FromClaimIDs {
		claim := b.findClaim(claimID)
		if claim == nil {
			return fmt.Errorf("intent source claim %q does not exist", claimID)
		}
		if claim.Kind == ClaimGoal {
			return fmt.Errorf("goal claim cannot be an intent source")
		}
	}
	return nil
}

func (b *boardState) claimIntent(intentID, executorID string, at time.Time) (Intent, error) {
	intent := b.findIntent(intentID)
	if intent == nil {
		return Intent{}, fmt.Errorf("intent %q not found", intentID)
	}
	if err := ValidateIntentTransition(intent.State, IntentClaimed); err != nil {
		return Intent{}, err
	}
	if b.claimedCount() >= executorSlots {
		return Intent{}, fmt.Errorf("%w: executor slot is occupied", ErrIntentCapacity)
	}
	if executorID == "" {
		return Intent{}, fmt.Errorf("executor id is required")
	}
	intent.State = IntentClaimed
	intent.ExecutorID = executorID
	intent.ClaimedAt = timePtr(at)
	b.version++
	return *intent, nil
}

func (b *boardState) returnIntentToOpen(intentID string, at time.Time) (Intent, error) {
	intent := b.findIntent(intentID)
	if intent == nil {
		return Intent{}, fmt.Errorf("intent %q not found", intentID)
	}
	if err := ValidateIntentTransition(intent.State, IntentOpen); err != nil {
		return Intent{}, err
	}
	intent.State = IntentOpen
	intent.ExecutorID = ""
	intent.ClaimedAt = nil
	b.version++
	return *intent, nil
}

func (b *boardState) concludeIntent(intentID, claimID string, at time.Time) (Intent, error) {
	intent := b.findIntent(intentID)
	if intent == nil {
		return Intent{}, fmt.Errorf("intent %q not found", intentID)
	}
	if err := ValidateIntentTransition(intent.State, IntentConcluded); err != nil {
		return Intent{}, err
	}
	claim := b.findClaim(claimID)
	if claim == nil {
		return Intent{}, fmt.Errorf("result claim %q does not exist", claimID)
	}
	intent.State = IntentConcluded
	intent.ResultClaimID = claimID
	intent.ConcludedAt = timePtr(at)
	b.version++
	return *intent, nil
}

func (b *boardState) abandonIntent(intentID, reason string, at time.Time) (Intent, error) {
	intent := b.findIntent(intentID)
	if intent == nil {
		return Intent{}, fmt.Errorf("intent %q not found", intentID)
	}
	if err := ValidateIntentTransition(intent.State, IntentAbandoned); err != nil {
		return Intent{}, err
	}
	intent.State = IntentAbandoned
	intent.Error = reason
	intent.ConcludedAt = timePtr(at)
	b.version++
	return *intent, nil
}

func (b *boardState) openCount() int {
	count := 0
	for _, intent := range b.intents {
		if intent.State == IntentOpen {
			count++
		}
	}
	return count
}

func (b *boardState) claimedCount() int {
	count := 0
	for _, intent := range b.intents {
		if intent.State == IntentClaimed {
			count++
		}
	}
	return count
}

func (b *boardState) boardRevision() uint64 { return b.version }

// restoreBoardState rebuilds one Run's in-memory board from the newest
// canonical commit. Every replayed record is re-validated; a claimed intent is
// returned to open because a resumed process has no executor attempt in
// flight. The restored revision continues after the replayed one so the next
// canonical commit stays strictly monotonic.
func restoreBoardState(runID string, spec RunSpec, target TargetBinding, commit Commit) (*boardState, error) {
	board := &boardState{runID: runID, task: spec.Task, target: target, policy: NormalizeRunPolicy(spec.Policy)}
	for _, claim := range commit.Claims {
		if _, err := board.addClaim(claim); err != nil {
			return nil, fmt.Errorf("restore claim %s: %w", claim.ID, err)
		}
	}
	for _, intent := range commit.Intents {
		if err := board.validateIntentClaims(intent); err != nil {
			return nil, fmt.Errorf("restore intent %s: %w", intent.ID, err)
		}
		if err := ValidateIntent(intent); err != nil {
			return nil, fmt.Errorf("restore intent %s: %w", intent.ID, err)
		}
		b := &board.intents
		*b = append(*b, intent)
	}
	for _, attempt := range commit.Attempts {
		if attempt.RunID != runID {
			return nil, fmt.Errorf("restore attempt %s belongs to run %q", attempt.ID, attempt.RunID)
		}
		board.attempts = append(board.attempts, attempt)
	}
	for _, observation := range commit.Observations {
		if observation.RunID != runID {
			return nil, fmt.Errorf("restore observation %s belongs to run %q", observation.ID, observation.RunID)
		}
		board.obs = append(board.obs, observation)
	}
	for _, artifact := range commit.Artifacts {
		if artifact.RunID != runID {
			return nil, fmt.Errorf("restore artifact %s belongs to run %q", artifact.ID, artifact.RunID)
		}
		board.artifacts = append(board.artifacts, artifact)
	}
	// A crash can leave an intent claimed with no attempt in flight; the
	// resumed executor slot is free, so return it to open.
	for i := range board.intents {
		if board.intents[i].State == IntentClaimed {
			if err := ValidateIntentTransition(IntentClaimed, IntentOpen); err != nil {
				return nil, fmt.Errorf("restore intent %s: %w", board.intents[i].ID, err)
			}
			board.intents[i].State = IntentOpen
			board.intents[i].ExecutorID = ""
			board.intents[i].ClaimedAt = nil
		}
	}
	if commit.Revision == 0 {
		return nil, errors.New("restored board commit has no revision")
	}
	board.version = commit.Revision
	// Continue the ID sequence past every replayed record so restored runs
	// cannot collide with historical identifiers.
	for _, claim := range board.claims {
		board.seq = maxInt(board.seq, seqOf(claim.ID))
	}
	for _, intent := range board.intents {
		board.seq = maxInt(board.seq, seqOf(intent.ID))
	}
	for _, attempt := range board.attempts {
		board.seq = maxInt(board.seq, seqOf(attempt.ID))
	}
	for _, observation := range board.obs {
		board.seq = maxInt(board.seq, seqOf(observation.ID))
	}
	return board, nil
}

func seqOf(id string) int {
	_, tail, found := strings.Cut(id, ":")
	if !found {
		return 0
	}
	n, err := strconv.Atoi(strings.Clone(tail))
	if err != nil {
		return 0
	}
	return n
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// nextOpenIntent returns the first open Intent without cloning the board.
func (b *boardState) nextOpenIntent() (Intent, bool) {
	for _, intent := range b.intents {
		if intent.State == IntentOpen {
			return intent, true
		}
	}
	return Intent{}, false
}

func (b *boardState) findIntent(id string) *Intent {
	for i := range b.intents {
		if b.intents[i].ID == id {
			return &b.intents[i]
		}
	}
	return nil
}

func (b *boardState) findClaim(id string) *Claim {
	for i := range b.claims {
		if b.claims[i].ID == id {
			return &b.claims[i]
		}
	}
	return nil
}

// admitReasonIntents validates a complete Reason proposal before mutating the
// board, so stale or oversized proposals cannot leave partial work behind.
func admitReasonIntents(board *boardState, report ReasonReport, canCreateIntent bool) ([]Intent, error) {
	if report.BoardRevision != board.boardRevision() {
		return nil, fmt.Errorf("%w: stale board revision %d, want %d", ErrIntentCapacity, report.BoardRevision, board.boardRevision())
	}
	if board.openCount() > 0 {
		return nil, fmt.Errorf("%w: reason cannot create while an open intent exists", ErrIntentCapacity)
	}
	if err := ValidateReasonReport(report, canCreateIntent); err != nil {
		return nil, err
	}
	if report.GoalCandidate != nil {
		return nil, nil
	}
	if canCreateIntent && len(report.Intents) == 0 {
		return nil, fmt.Errorf("reason produced no intents for the free slot")
	}
	planned := make([]Intent, 0, len(report.Intents))
	for _, proposal := range report.Intents {
		intent := Intent{
			ID:           board.nextID("intent"),
			RunID:        board.runID,
			FromClaimIDs: append([]string(nil), proposal.From...),
			Description:  proposal.Description,
			State:        IntentOpen,
			CreatedAt:    time.Now().UTC(),
		}
		if err := board.validateIntentClaims(intent); err != nil {
			return nil, err
		}
		if err := ValidateIntent(intent); err != nil {
			return nil, err
		}
		planned = append(planned, intent)
	}
	if len(board.intents)+len(planned) > board.maxTotal() {
		return nil, fmt.Errorf("max total intents reached")
	}
	if board.openCount()+board.claimedCount()+len(planned) > executorSlots {
		return nil, fmt.Errorf("%w: proposal exceeds open capacity", ErrIntentCapacity)
	}
	board.intents = append(board.intents, planned...)
	board.version++
	return planned, nil
}

func timePtr(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value
	return &copy
}

func cloneClaims(values []Claim) []Claim {
	out := make([]Claim, len(values))
	for i := range values {
		out[i] = values[i]
		out[i].ObservationIDs = append([]string(nil), values[i].ObservationIDs...)
		out[i].ArtifactIDs = append([]string(nil), values[i].ArtifactIDs...)
	}
	return out
}

func cloneIntents(values []Intent) []Intent {
	out := make([]Intent, len(values))
	for i := range values {
		out[i] = values[i]
		out[i].FromClaimIDs = append([]string(nil), values[i].FromClaimIDs...)
		out[i].AttemptIDs = append([]string(nil), values[i].AttemptIDs...)
		if values[i].ClaimedAt != nil {
			at := *values[i].ClaimedAt
			out[i].ClaimedAt = &at
		}
		if values[i].ConcludedAt != nil {
			at := *values[i].ConcludedAt
			out[i].ConcludedAt = &at
		}
	}
	return out
}

func cloneObservations(values []Observation) []Observation {
	out := make([]Observation, len(values))
	for i := range values {
		out[i] = values[i]
		out[i].Evidence = append([]Artifact(nil), values[i].Evidence...)
	}
	return out
}

func (b *boardState) addObservation(observation Observation) {
	b.obs = append(b.obs, observation)
}

func (b *boardState) addArtifact(artifact Artifact) {
	b.artifacts = append(b.artifacts, artifact)
}

func (b *boardState) addAttempt(attempt IntentAttempt) {
	b.attempts = append(b.attempts, attempt)
}

func (b *boardState) registerAttempt(intentID, attemptID string) (Intent, error) {
	intent := b.findIntent(intentID)
	if intent == nil {
		return Intent{}, fmt.Errorf("intent %q not found", intentID)
	}
	intent.AttemptIDs = append(intent.AttemptIDs, attemptID)
	b.version++
	return *intent, nil
}

func (b *boardState) updateAttempt(attempt IntentAttempt) {
	for i := range b.attempts {
		if b.attempts[i].ID == attempt.ID {
			b.attempts[i] = attempt
			return
		}
	}
	b.attempts = append(b.attempts, attempt)
}
