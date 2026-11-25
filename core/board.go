package core

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Board is the deterministic projection of a Run journal's Claim/Intent and
// evidence records. Revision increments once per board-changing record.
type Board struct {
	RunID        string          `json:"run_id"`
	Revision     uint64          `json:"revision"`
	Claims       []Claim         `json:"claims,omitempty"`
	Intents      []Intent        `json:"intents,omitempty"`
	Attempts     []IntentAttempt `json:"attempts,omitempty"`
	Observations []Observation   `json:"observations,omitempty"`
	Artifacts    []Artifact      `json:"artifacts,omitempty"`
}

// NewBoard returns an empty board for one Run.
func NewBoard(runID string) *Board { return &Board{RunID: runID} }

// Apply applies one journal record. Non-board records are valid replay input
// and leave the board unchanged.
func (b *Board) Apply(record Record) error {
	if b == nil {
		return errors.New("board is required")
	}
	if record.RunID != b.RunID {
		return fmt.Errorf("record run_id %q does not match board %q", record.RunID, b.RunID)
	}
	payload, err := DecodeRecord(record)
	if err != nil {
		return err
	}
	switch p := payload.(type) {
	case ClaimCreatedRecord:
		return b.addClaim(p.Claim)
	case IntentProposedRecord:
		return b.addIntent(p.Intent)
	case IntentClaimedRecord:
		return b.claimIntent(p)
	case IntentConcludedRecord:
		return b.concludeIntent(p)
	case IntentAbandonedRecord:
		return b.abandonIntent(p)
	case AttemptStartedRecord:
		return b.addAttempt(p.Attempt)
	case AttemptConcludedRecord:
		return b.updateAttempt(p.AttemptID, IntentAttemptConcluded, p.Conclusion, "", p.At)
	case AttemptFailedRecord:
		return b.updateAttempt(p.AttemptID, IntentAttemptFailed, "", p.Error, p.At)
	case AttemptCancelledRecord:
		return b.updateAttempt(p.AttemptID, IntentAttemptCancelled, "", "", p.At)
	case AttemptTimedOutRecord:
		return b.updateAttempt(p.AttemptID, IntentAttemptTimedOut, "", "", p.At)
	case ObservationCreatedRecord:
		return b.addObservation(p.Observation)
	case ArtifactStoredRecord:
		return b.addArtifact(p.Artifact)
	default:
		return nil
	}
}

// ApplyAll applies a record sequence and validates the resulting board.
func (b *Board) ApplyAll(records []Record) error {
	for _, record := range records {
		if err := b.Apply(record); err != nil {
			return err
		}
	}
	return b.Validate()
}

func (b *Board) addClaim(claim Claim) error {
	if err := ValidateClaim(claim); err != nil {
		return err
	}
	if _, _, ok := b.findClaim(claim.ID); ok {
		return fmt.Errorf("duplicate claim id %q", claim.ID)
	}
	b.Claims = append(b.Claims, claim)
	b.Revision++
	return nil
}

func (b *Board) addIntent(intent Intent) error {
	if err := ValidateIntent(intent); err != nil {
		return err
	}
	if _, _, ok := b.findIntent(intent.ID); ok {
		return fmt.Errorf("duplicate intent id %q", intent.ID)
	}
	b.Intents = append(b.Intents, intent)
	b.Revision++
	return nil
}

func (b *Board) claimIntent(p IntentClaimedRecord) error {
	i, intent, ok := b.findIntent(p.IntentID)
	if !ok {
		return fmt.Errorf("intent %q not found", p.IntentID)
	}
	if intent.State != IntentOpen {
		return fmt.Errorf("intent %q is %s, not open", intent.ID, intent.State)
	}
	if strings.TrimSpace(p.ExecutorID) == "" {
		return errors.New("intent claim requires executor_id")
	}
	at := p.At.UTC()
	intent.State = IntentClaimed
	intent.ExecutorID = p.ExecutorID
	intent.ClaimedAt = &at
	b.Intents[i] = intent
	b.Revision++
	return nil
}

func (b *Board) concludeIntent(p IntentConcludedRecord) error {
	i, intent, ok := b.findIntent(p.IntentID)
	if !ok {
		return fmt.Errorf("intent %q not found", p.IntentID)
	}
	if intent.State != IntentClaimed {
		return fmt.Errorf("intent %q is %s, not claimed", intent.ID, intent.State)
	}
	if _, _, ok := b.findClaim(p.ResultClaimID); !ok {
		return fmt.Errorf("result claim %q not found", p.ResultClaimID)
	}
	at := p.At.UTC()
	intent.State = IntentConcluded
	intent.ResultClaimID = p.ResultClaimID
	intent.ConcludedAt = &at
	b.Intents[i] = intent
	b.Revision++
	return nil
}

func (b *Board) abandonIntent(p IntentAbandonedRecord) error {
	i, intent, ok := b.findIntent(p.IntentID)
	if !ok {
		return fmt.Errorf("intent %q not found", p.IntentID)
	}
	if intent.State != IntentOpen && intent.State != IntentClaimed {
		return fmt.Errorf("intent %q is %s and cannot be abandoned", intent.ID, intent.State)
	}
	intent.State = IntentAbandoned
	intent.Error = p.Reason
	b.Intents[i] = intent
	b.Revision++
	return nil
}

func (b *Board) addAttempt(attempt IntentAttempt) error {
	if err := validateAttempt(attempt); err != nil {
		return err
	}
	if _, _, ok := b.findAttempt(attempt.ID); ok {
		return fmt.Errorf("duplicate attempt id %q", attempt.ID)
	}
	b.Attempts = append(b.Attempts, attempt)
	b.Revision++
	return nil
}

func (b *Board) updateAttempt(id string, phase IntentAttemptPhase, conclusion, failure string, at time.Time) error {
	i, attempt, ok := b.findAttempt(id)
	if !ok {
		return fmt.Errorf("attempt %q not found", id)
	}
	if attempt.Phase != IntentAttemptRunning {
		return fmt.Errorf("attempt %q is %s, not running", id, attempt.Phase)
	}
	attempt.Phase = phase
	attempt.Conclusion = conclusion
	attempt.Error = failure
	attempt.FinishedAt = at.UTC()
	b.Attempts[i] = attempt
	b.Revision++
	return nil
}

func (b *Board) addObservation(observation Observation) error {
	if err := validateObservation(observation); err != nil {
		return err
	}
	if _, _, ok := b.findObservation(observation.ID); ok {
		return fmt.Errorf("duplicate observation id %q", observation.ID)
	}
	b.Observations = append(b.Observations, observation)
	b.Revision++
	return nil
}

func (b *Board) addArtifact(artifact Artifact) error {
	if err := validateArtifact(artifact); err != nil {
		return err
	}
	for _, existing := range b.Artifacts {
		if existing.ID == artifact.ID && existing.AttemptID == artifact.AttemptID && existing.Name == artifact.Name {
			return fmt.Errorf("duplicate artifact %q for attempt %q", artifact.Name, artifact.AttemptID)
		}
	}
	b.Artifacts = append(b.Artifacts, artifact)
	b.Revision++
	return nil
}

// Validate checks identity, lifecycle and evidence references.
func (b *Board) Validate() error {
	if b == nil {
		return errors.New("board is required")
	}
	if strings.TrimSpace(b.RunID) == "" {
		return errors.New("board run_id is required")
	}
	claims := map[string]Claim{}
	for _, claim := range b.Claims {
		if err := ValidateClaim(claim); err != nil {
			return err
		}
		if claim.RunID != b.RunID {
			return fmt.Errorf("claim %q run_id %q does not match board %q", claim.ID, claim.RunID, b.RunID)
		}
		if _, ok := claims[claim.ID]; ok {
			return fmt.Errorf("duplicate claim id %q", claim.ID)
		}
		claims[claim.ID] = claim
	}
	intents := map[string]Intent{}
	for _, intent := range b.Intents {
		if err := ValidateIntent(intent); err != nil {
			return err
		}
		if intent.RunID != b.RunID {
			return fmt.Errorf("intent %q run_id %q does not match board %q", intent.ID, intent.RunID, b.RunID)
		}
		if _, ok := intents[intent.ID]; ok {
			return fmt.Errorf("duplicate intent id %q", intent.ID)
		}
		for _, claimID := range intent.FromClaimIDs {
			if _, ok := claims[claimID]; !ok {
				return fmt.Errorf("intent %q references missing claim %q", intent.ID, claimID)
			}
		}
		if intent.State == IntentClaimed || intent.State == IntentConcluded {
			if strings.TrimSpace(intent.ExecutorID) == "" {
				return fmt.Errorf("intent %q has no executor", intent.ID)
			}
			if intent.ClaimedAt == nil {
				return fmt.Errorf("intent %q has no claimed_at", intent.ID)
			}
		}
		if intent.State == IntentConcluded {
			if intent.ConcludedAt == nil {
				return fmt.Errorf("intent %q has no concluded_at", intent.ID)
			}
			if _, ok := claims[intent.ResultClaimID]; !ok {
				return fmt.Errorf("intent %q references missing result claim %q", intent.ID, intent.ResultClaimID)
			}
		}
		intents[intent.ID] = intent
	}
	attempts := map[string]IntentAttempt{}
	for _, attempt := range b.Attempts {
		if err := validateAttempt(attempt); err != nil {
			return err
		}
		if attempt.RunID != b.RunID {
			return fmt.Errorf("attempt %q run_id %q does not match board %q", attempt.ID, attempt.RunID, b.RunID)
		}
		if _, ok := attempts[attempt.ID]; ok {
			return fmt.Errorf("duplicate attempt id %q", attempt.ID)
		}
		if _, ok := intents[attempt.IntentID]; !ok {
			return fmt.Errorf("attempt %q references missing intent %q", attempt.ID, attempt.IntentID)
		}
		if attempt.Phase == IntentAttemptRunning {
			if !attempt.FinishedAt.IsZero() {
				return fmt.Errorf("running attempt %q has finished_at", attempt.ID)
			}
		} else if attempt.FinishedAt.IsZero() {
			return fmt.Errorf("finished attempt %q has no finished_at", attempt.ID)
		}
		attempts[attempt.ID] = attempt
	}
	observations := map[string]Observation{}
	for _, observation := range b.Observations {
		if err := validateObservation(observation); err != nil {
			return err
		}
		if observation.RunID != b.RunID {
			return fmt.Errorf("observation %q run_id %q does not match board %q", observation.ID, observation.RunID, b.RunID)
		}
		if _, ok := observations[observation.ID]; ok {
			return fmt.Errorf("duplicate observation id %q", observation.ID)
		}
		if _, ok := intents[observation.IntentID]; !ok {
			return fmt.Errorf("observation %q references missing intent %q", observation.ID, observation.IntentID)
		}
		if attempt, ok := attempts[observation.AttemptID]; !ok {
			return fmt.Errorf("observation %q references missing attempt %q", observation.ID, observation.AttemptID)
		} else if attempt.IntentID != observation.IntentID {
			return fmt.Errorf("observation %q intent %q does not match attempt intent %q", observation.ID, observation.IntentID, attempt.IntentID)
		}
		observations[observation.ID] = observation
	}
	artifacts := map[string]Artifact{}
	artifactKeys := map[string]bool{}
	for _, artifact := range b.Artifacts {
		if err := validateArtifact(artifact); err != nil {
			return err
		}
		if artifact.RunID != b.RunID {
			return fmt.Errorf("artifact %q run_id %q does not match board %q", artifact.ID, artifact.RunID, b.RunID)
		}
		key := artifact.ID + "\x00" + artifact.AttemptID + "\x00" + artifact.Name
		if artifactKeys[key] {
			return fmt.Errorf("duplicate artifact %q for attempt %q", artifact.Name, artifact.AttemptID)
		}
		artifactKeys[key] = true
		artifacts[artifact.ID] = artifact
	}
	for _, claim := range b.Claims {
		if claim.SourceIntentID != "" {
			if _, ok := intents[claim.SourceIntentID]; !ok {
				return fmt.Errorf("claim %q references missing source intent %q", claim.ID, claim.SourceIntentID)
			}
		}
		for _, observationID := range claim.ObservationIDs {
			if _, ok := observations[observationID]; !ok {
				return fmt.Errorf("claim %q references missing observation %q", claim.ID, observationID)
			}
		}
		for _, artifactID := range claim.ArtifactIDs {
			if _, ok := artifacts[artifactID]; !ok {
				return fmt.Errorf("claim %q references missing artifact %q", claim.ID, artifactID)
			}
		}
	}
	for _, observation := range b.Observations {
		for _, artifact := range observation.Evidence {
			if !b.hasArtifact(artifact.ID, observation.AttemptID) {
				return fmt.Errorf("observation %q references missing artifact %q for attempt %q", observation.ID, artifact.ID, observation.AttemptID)
			}
		}
	}
	return nil
}

func (b *Board) hasArtifact(id, attemptID string) bool {
	for _, artifact := range b.Artifacts {
		if artifact.ID == id && artifact.AttemptID == attemptID {
			return true
		}
	}
	return false
}

func (b *Board) findClaim(id string) (int, Claim, bool) {
	for i, claim := range b.Claims {
		if claim.ID == id {
			return i, claim, true
		}
	}
	return 0, Claim{}, false
}

func (b *Board) findIntent(id string) (int, Intent, bool) {
	for i, intent := range b.Intents {
		if intent.ID == id {
			return i, intent, true
		}
	}
	return 0, Intent{}, false
}

func (b *Board) findAttempt(id string) (int, IntentAttempt, bool) {
	for i, attempt := range b.Attempts {
		if attempt.ID == id {
			return i, attempt, true
		}
	}
	return 0, IntentAttempt{}, false
}

func (b *Board) findObservation(id string) (int, Observation, bool) {
	for i, observation := range b.Observations {
		if observation.ID == id {
			return i, observation, true
		}
	}
	return 0, Observation{}, false
}

func validateAttempt(attempt IntentAttempt) error {
	if strings.TrimSpace(attempt.ID) == "" || strings.TrimSpace(attempt.RunID) == "" ||
		strings.TrimSpace(attempt.IntentID) == "" || strings.TrimSpace(attempt.SessionID) == "" {
		return errors.New("attempt id, run_id, intent_id and session_id are required")
	}
	switch attempt.Phase {
	case IntentAttemptRunning, IntentAttemptConcluded, IntentAttemptFailed, IntentAttemptCancelled, IntentAttemptTimedOut:
		return nil
	default:
		return fmt.Errorf("unknown attempt phase %q", attempt.Phase)
	}
}

func validateObservation(observation Observation) error {
	if strings.TrimSpace(observation.ID) == "" || strings.TrimSpace(observation.RunID) == "" ||
		strings.TrimSpace(observation.IntentID) == "" || strings.TrimSpace(observation.AttemptID) == "" ||
		strings.TrimSpace(observation.Kind) == "" || strings.TrimSpace(observation.Target) == "" {
		return errors.New("observation id, run_id, intent_id, attempt_id, kind and target are required")
	}
	if len(observation.Evidence) == 0 {
		return fmt.Errorf("observation %q requires evidence", observation.ID)
	}
	return nil
}

func validateArtifact(artifact Artifact) error {
	if strings.TrimSpace(artifact.ID) == "" || strings.TrimSpace(artifact.RunID) == "" ||
		strings.TrimSpace(artifact.AttemptID) == "" || strings.TrimSpace(artifact.Name) == "" {
		return errors.New("artifact id, run_id, attempt_id and name are required")
	}
	if len(artifact.SHA256) != 64 {
		return fmt.Errorf("artifact %q sha256 must be 64 hex characters", artifact.ID)
	}
	if artifact.ID != artifact.SHA256 {
		return fmt.Errorf("artifact %q id does not match content hash %q", artifact.ID, artifact.SHA256)
	}
	if artifact.Size < 0 {
		return fmt.Errorf("artifact %q size must not be negative", artifact.ID)
	}
	return nil
}
