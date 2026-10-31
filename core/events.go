package core

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"time"
)

// EventType is the versioned canonical audit vocabulary.
type EventType string

const (
	EventRunCreated            EventType = "run.created"
	EventRunPhase              EventType = "run.phase"
	EventRunProvisioned        EventType = "run.provisioned"
	EventRunFinalized          EventType = "run.finalized"
	EventAgentSessionStarted   EventType = "agent.session.started"
	EventAgent                 EventType = "agent.event"
	EventContextTruncated      EventType = "context.truncated"
	EventArtifactStored        EventType = "artifact.stored"
	EventObservationCreated    EventType = "observation.created"
	EventAuthorityProgress     EventType = "authority.progress"
	EventAuthorityCompleted    EventType = "authority.completed"
	EventAuthorityObserveError EventType = "authority.observe_error"
	EventClaimCreated          EventType = "claim.created"
	EventReasonStarted         EventType = "reason.started"
	EventReasonCompleted       EventType = "reason.completed"
	EventReasonRejected        EventType = "reason.rejected"
	EventIntentCreated         EventType = "intent.created"
	EventIntentClaimed         EventType = "intent.claimed"
	EventIntentReleased        EventType = "intent.released"
	EventIntentConcluded       EventType = "intent.concluded"
	EventIntentAbandoned       EventType = "intent.abandoned"
	EventIntentRejected        EventType = "intent.rejected"
	EventAttemptStarted        EventType = "attempt.started"
	EventAttemptConcluded      EventType = "attempt.concluded"
	EventAttemptFailed         EventType = "attempt.failed"
)

type RunCreatedPayload struct {
	TaskID   string `json:"task_id"`
	TargetID string `json:"target_id"`
}

type RunPhasePayload struct {
	Phase RunPhase `json:"phase"`
}

type RunProvisionedPayload struct {
	Target TargetBinding `json:"target"`
}

type AgentSessionStartedPayload struct {
	SessionID string    `json:"session_id"`
	Role      AgentRole `json:"role"`
}

type AgentEventPayload struct {
	NativeType string          `json:"native_type"`
	Data       json.RawMessage `json:"data,omitempty"`
}

type ContextTruncatedPayload struct {
	Context    string `json:"context"`
	AttemptID  string `json:"attempt_id,omitempty"`
	Candidates int    `json:"candidates,omitempty"`
	Kept       int    `json:"kept,omitempty"`
	Dropped    int    `json:"dropped"`
}

type ArtifactStoredPayload struct {
	Artifact Artifact `json:"artifact"`
}

type ObservationCreatedPayload struct {
	Observation Observation `json:"observation"`
}

type AuthorityProgressPayload struct {
	Completion Completion `json:"completion"`
}

type AuthorityErrorPayload struct {
	Error string `json:"error"`
}

type ClaimCreatedPayload struct {
	Claim Claim `json:"claim"`
}

type IntentPayload struct {
	Intent Intent `json:"intent"`
}

type IntentAttemptPayload struct {
	Attempt IntentAttempt `json:"attempt"`
}

type ReasonPayload struct {
	BoardRevision   uint64           `json:"board_revision"`
	CanCreateIntent bool             `json:"can_create_intent,omitempty"`
	Intents         []IntentProposal `json:"intents,omitempty"`
	GoalCandidate   *GoalCandidate   `json:"goal_candidate,omitempty"`
	Error           string           `json:"error,omitempty"`
}

type IntentRejectedPayload struct {
	BoardRevision uint64           `json:"board_revision"`
	Intents       []IntentProposal `json:"intents,omitempty"`
	GoalCandidate *GoalCandidate   `json:"goal_candidate,omitempty"`
	Reason        string           `json:"reason"`
}

type RunFinalizedPayload struct {
	Outcome RunOutcome `json:"outcome"`
	Error   string     `json:"error,omitempty"`
}

type eventContract struct {
	version int
	payload any
}

var eventContracts = map[EventType]eventContract{
	EventRunCreated:            {version: 1, payload: RunCreatedPayload{}},
	EventRunPhase:              {version: 1, payload: RunPhasePayload{}},
	EventRunProvisioned:        {version: 2, payload: RunProvisionedPayload{}},
	EventRunFinalized:          {version: 1, payload: RunFinalizedPayload{}},
	EventAgentSessionStarted:   {version: 1, payload: AgentSessionStartedPayload{}},
	EventAgent:                 {version: 2, payload: AgentEventPayload{}},
	EventContextTruncated:      {version: 1, payload: ContextTruncatedPayload{}},
	EventArtifactStored:        {version: 1, payload: ArtifactStoredPayload{}},
	EventObservationCreated:    {version: 1, payload: ObservationCreatedPayload{}},
	EventAuthorityProgress:     {version: 1, payload: AuthorityProgressPayload{}},
	EventAuthorityCompleted:    {version: 1, payload: AuthorityProgressPayload{}},
	EventAuthorityObserveError: {version: 1, payload: AuthorityErrorPayload{}},
	EventClaimCreated:          {version: 1, payload: ClaimCreatedPayload{}},
	EventReasonStarted:         {version: 2, payload: ReasonPayload{}},
	EventReasonCompleted:       {version: 2, payload: ReasonPayload{}},
	EventReasonRejected:        {version: 2, payload: ReasonPayload{}},
	EventIntentCreated:         {version: 2, payload: IntentPayload{}},
	EventIntentClaimed:         {version: 2, payload: IntentPayload{}},
	EventIntentReleased:        {version: 2, payload: IntentPayload{}},
	EventIntentConcluded:       {version: 2, payload: IntentPayload{}},
	EventIntentAbandoned:       {version: 2, payload: IntentPayload{}},
	EventIntentRejected:        {version: 2, payload: IntentRejectedPayload{}},
	EventAttemptStarted:        {version: 2, payload: IntentAttemptPayload{}},
	EventAttemptConcluded:      {version: 2, payload: IntentAttemptPayload{}},
	EventAttemptFailed:         {version: 2, payload: IntentAttemptPayload{}},
}

// EventTypes returns the canonical event vocabulary in deterministic order.
func EventTypes() []EventType {
	out := make([]EventType, 0, len(eventContracts))
	for typ := range eventContracts {
		out = append(out, typ)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// EventSchemaVersion returns the current schema version for a registered event.
func EventSchemaVersion(typ EventType) (int, bool) {
	contract, ok := eventContracts[typ]
	if !ok {
		return 0, false
	}
	return contract.version, true
}

// NewEvent builds a typed, versioned canonical event.
func NewEvent(runID string, typ EventType, payload any) (Event, error) {
	contract, ok := eventContracts[typ]
	if !ok {
		return Event{}, fmt.Errorf("unknown event type %q", typ)
	}
	if reflect.TypeOf(payload) != reflect.TypeOf(contract.payload) {
		return Event{}, fmt.Errorf("event %q payload type %T does not match %T", typ, payload, contract.payload)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("marshal event %q: %w", typ, err)
	}
	return Event{RunID: runID, SchemaVersion: contract.version, Type: typ, Time: time.Now().UTC(), Data: data}, nil
}
