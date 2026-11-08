package engine

import "github.com/red-copilot/harness/core"

// Core domain aliases keep the implementation readable while the canonical
// contracts live in package core.
type (
	AgentDescriptor            = core.AgentDescriptor
	AgentEvent                 = core.AgentEvent
	AgentEventKind             = core.AgentEventKind
	AgentEventPayload          = core.AgentEventPayload
	AgentRole                  = core.AgentRole
	AgentSessionStartedPayload = core.AgentSessionStartedPayload
	Artifact                   = core.Artifact
	ArtifactInput              = core.ArtifactInput
	ArtifactStoredPayload      = core.ArtifactStoredPayload
	AttemptBudget              = core.AttemptBudget
	AuthorityErrorPayload      = core.AuthorityErrorPayload
	AuthorityProgressPayload   = core.AuthorityProgressPayload
	Budget                     = core.Budget
	Completion                 = core.Completion
	ContextPolicy              = core.ContextPolicy
	ContextTruncatedPayload    = core.ContextTruncatedPayload
	Event                      = core.Event
	EventType                  = core.EventType
	EvidenceArchiveRequest     = core.EvidenceArchiveRequest
	EvidencePolicy             = core.EvidencePolicy
	ExitStatus                 = core.ExitStatus
	ExplorationPolicy          = core.ExplorationPolicy
	AttemptReport              = core.AttemptReport
	ExploreRequest             = core.ExploreRequest
	ExploreStrategy            = core.ExploreStrategy
	Claim                      = core.Claim
	ClaimCreatedPayload        = core.ClaimCreatedPayload
	ClaimKind                  = core.ClaimKind
	ClaimProposal              = core.ClaimProposal
	GoalCandidate              = core.GoalCandidate
	Intent                     = core.Intent
	IntentAttempt              = core.IntentAttempt
	IntentAttemptPayload       = core.IntentAttemptPayload
	IntentAttemptPhase         = core.IntentAttemptPhase
	IntentPayload              = core.IntentPayload
	IntentProposal             = core.IntentProposal
	IntentRejectedPayload      = core.IntentRejectedPayload
	IntentState                = core.IntentState
	IsolationMode              = core.IsolationMode
	LogEvent                   = core.LogEvent
	LogRecord                  = core.LogRecord
	Mount                      = core.Mount
	Observation                = core.Observation
	ObservationCreatedPayload  = core.ObservationCreatedPayload
	ObservationProposal        = core.ObservationProposal
	Process                    = core.Process
	ProcessSpec                = core.ProcessSpec
	ProvisionRequest           = core.ProvisionRequest
	ReasonPayload              = core.ReasonPayload
	ReasonReport               = core.ReasonReport
	ReasonRequest              = core.ReasonRequest
	ReasonStrategy             = core.ReasonStrategy
	Redactor                   = core.Redactor
	ResourceSpec               = core.ResourceSpec
	Result                     = core.Result
	RunCreatedPayload          = core.RunCreatedPayload
	RunDescriptor              = core.RunDescriptor
	RunFinalizedPayload        = core.RunFinalizedPayload
	RunOutcome                 = core.RunOutcome
	RunPhase                   = core.RunPhase
	RunPhasePayload            = core.RunPhasePayload
	RunPolicy                  = core.RunPolicy
	RunProvisionedPayload      = core.RunProvisionedPayload
	RunRecord                  = core.RunRecord
	SessionResult              = core.SessionResult
	SessionStatus              = core.SessionStatus
	TargetBinding              = core.TargetBinding
	TargetRuntime              = core.TargetRuntime
	TargetSpec                 = core.TargetSpec
	Task                       = core.Task
	TurnLimits                 = core.TurnLimits
	TurnResult                 = core.TurnResult
	Usage                      = core.Usage
	Verdict                    = core.Verdict
	VerdictAuthority           = core.VerdictAuthority
	VerdictRecord              = core.VerdictRecord
	VerifyRequest              = core.VerifyRequest
)

const (
	CurrentSchemaVersion = core.CurrentSchemaVersion

	SessionRunning   = core.SessionRunning
	SessionSucceeded = core.SessionSucceeded
	SessionFailed    = core.SessionFailed
	SessionCancelled = core.SessionCancelled

	RunPhaseQueued       = core.RunPhaseQueued
	RunPhaseProvisioning = core.RunPhaseProvisioning
	RunPhaseRunning      = core.RunPhaseRunning
	RunPhaseFinalizing   = core.RunPhaseFinalizing
	RunPhaseVerifying    = core.RunPhaseVerifying
	RunPhaseTerminal     = core.RunPhaseTerminal

	RunOutcomeSucceeded      = core.RunOutcomeSucceeded
	RunOutcomeFailed         = core.RunOutcomeFailed
	RunOutcomeCancelled      = core.RunOutcomeCancelled
	RunOutcomeTimedOut       = core.RunOutcomeTimedOut
	RunOutcomeInterrupted    = core.RunOutcomeInterrupted
	RunOutcomeStagnated      = core.RunOutcomeStagnated
	VerdictAuthorityVerifier = core.VerdictAuthorityVerifier
	VerdictAuthorityBackend  = core.VerdictAuthorityBackend

	IsolationCallerManaged = core.IsolationCallerManaged
	AgentRoleReasoner      = core.AgentRoleReasoner
	AgentRoleExecutor      = core.AgentRoleExecutor

	AgentEventMessage   = core.AgentEventMessage
	AgentEventToolStart = core.AgentEventToolStart
	AgentEventToolEnd   = core.AgentEventToolEnd
	AgentEventSettled   = core.AgentEventSettled
	AgentEventEnd       = core.AgentEventEnd
	AgentEventError     = core.AgentEventError

	ClaimOrigin   = core.ClaimOrigin
	ClaimGoal     = core.ClaimGoal
	ClaimFinding  = core.ClaimFinding
	ClaimNegative = core.ClaimNegative

	IntentOpen      = core.IntentOpen
	IntentClaimed   = core.IntentClaimed
	IntentConcluded = core.IntentConcluded
	IntentAbandoned = core.IntentAbandoned

	IntentAttemptRunning   = core.IntentAttemptRunning
	IntentAttemptConcluded = core.IntentAttemptConcluded
	IntentAttemptFailed    = core.IntentAttemptFailed
	IntentAttemptCancelled = core.IntentAttemptCancelled
	IntentAttemptTimedOut  = core.IntentAttemptTimedOut

	EventRunCreated            = core.EventRunCreated
	EventRunPhase              = core.EventRunPhase
	EventRunProvisioned        = core.EventRunProvisioned
	EventRunFinalized          = core.EventRunFinalized
	EventAgentSessionStarted   = core.EventAgentSessionStarted
	EventAgent                 = core.EventAgent
	EventContextTruncated      = core.EventContextTruncated
	EventArtifactStored        = core.EventArtifactStored
	EventObservationCreated    = core.EventObservationCreated
	EventAuthorityProgress     = core.EventAuthorityProgress
	EventAuthorityCompleted    = core.EventAuthorityCompleted
	EventAuthorityObserveError = core.EventAuthorityObserveError
	EventClaimCreated          = core.EventClaimCreated
	EventReasonStarted         = core.EventReasonStarted
	EventReasonCompleted       = core.EventReasonCompleted
	EventReasonRejected        = core.EventReasonRejected
	EventIntentCreated         = core.EventIntentCreated
	EventIntentClaimed         = core.EventIntentClaimed
	EventIntentReleased        = core.EventIntentReleased
	EventIntentConcluded       = core.EventIntentConcluded
	EventIntentAbandoned       = core.EventIntentAbandoned
	EventIntentRejected        = core.EventIntentRejected
	EventAttemptStarted        = core.EventAttemptStarted
	EventAttemptConcluded      = core.EventAttemptConcluded
	EventAttemptFailed         = core.EventAttemptFailed
)

var (
	AuthorizeTarget          = core.AuthorizeTarget
	EventSchemaVersion       = core.EventSchemaVersion
	EventTypes               = core.EventTypes
	NewEvent                 = core.NewEvent
	NewRedactor              = core.NewRedactor
	NormalizeRunPolicy       = core.NormalizeRunPolicy
	TargetFingerprint        = core.TargetFingerprint
	ValidateDescriptor       = core.ValidateDescriptor
	ValidateAttemptReport    = core.ValidateAttemptReport
	ValidateClaim            = core.ValidateClaim
	ValidateIntent           = core.ValidateIntent
	ValidateIntentTransition = core.ValidateIntentTransition
	ValidateReasonReport     = core.ValidateReasonReport
	ValidateRunPolicy        = core.ValidateRunPolicy
	ValidateRunState         = core.ValidateRunState
	ValidateRunTransition    = core.ValidateRunTransition
	ValidateTargetBinding    = core.ValidateTargetBinding
	DefaultRunPolicy         = core.DefaultRunPolicy
)
