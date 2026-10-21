package harness

import (
	"context"
	"io"
)

// AgentRuntimeFactory creates runtime-neutral, reset-capable sessions.
type AgentRuntimeFactory interface {
	NewSession(context.Context, RuntimeRequest) (AgentSession, error)
}

// AgentSession is one untrusted runtime process. Turn completion is signaled
// only by agent_settled.
type AgentSession interface {
	Prompt(context.Context, string) error
	Steer(context.Context, string) error
	Reset(context.Context) error
	Events() <-chan AgentEvent
	Logs() <-chan LogEvent
	Usage() Usage
	Abort(context.Context) error
	Close(context.Context) error
}

type PlannerStrategy interface {
	Prompt(PlanRequest) (string, error)
	Parse(PlanRequest, TurnResult) (PlanProposal, error)
}

type ExecutorStrategy interface {
	Prompt(AttemptRequest) (string, error)
	Parse(AttemptRequest, TurnResult) (AttemptReport, error)
}

type ProcessStarter interface {
	Start(context.Context, ProcessSpec) (Process, error)
}

type ResourceCleaner interface {
	CleanupOrphans(context.Context) error
}

// TargetProvisioner creates and tears down the per-Run Target. Provisioning is
// not an authority: a started target says nothing about the security outcome.
type TargetProvisioner interface {
	Provision(context.Context, ProvisionRequest) (Target, error)
	Close(context.Context, Target) error
}

// CompletionSource is an authoritative external completion. Only a source that
// reports Done && Success can establish a successful Run.
type CompletionSource interface {
	Observe(context.Context, Target) (Completion, error)
}

// Backend is a provisioner that is also the authority for its Target.
type Backend interface {
	TargetProvisioner
	CompletionSource
}

type Verifier interface {
	Verify(context.Context, VerifyRequest) (Verdict, error)
}

type ArtifactStore interface {
	Put(context.Context, ArtifactInput) (Artifact, error)
	Open(context.Context, string) (io.ReadCloser, error)
}

// RunStore persists Run lifecycle records.
type RunStore interface {
	CreateRun(context.Context, RunRecord) error
	UpdateRun(context.Context, RunRecord) error
	GetRun(context.Context, string) (RunRecord, error)
	ListRuns(context.Context, int) ([]RunRecord, error)
	MarkInterrupted(context.Context) error
}

// AuditStore is the append-only audit stream: events, logs and verdicts.
type AuditStore interface {
	AppendEvent(context.Context, Event) (Event, error)
	ListEvents(context.Context, string, int64) ([]Event, error)
	AppendLog(context.Context, LogRecord) (LogRecord, error)
	ListLogs(context.Context, string, int64) ([]LogRecord, error)
	SaveVerdict(context.Context, VerdictRecord) error
	GetVerdict(context.Context, string) (VerdictRecord, error)
}

// ExplorationStore persists the hypothesis tree and its evidence links.
type ExplorationStore interface {
	SaveBranch(context.Context, Branch) error
	ListBranches(context.Context, string) ([]Branch, error)
	SaveAttempt(context.Context, Attempt) error
	ListAttempts(context.Context, string) ([]Attempt, error)
	SaveObservation(context.Context, Observation) error
	ListObservations(context.Context, string) ([]Observation, error)
	SaveArtifact(context.Context, Artifact) error
	ListArtifacts(context.Context, string) ([]Artifact, error)
}

// KnowledgeStore persists versioned Knowledge.
type KnowledgeStore interface {
	SaveKnowledge(context.Context, Knowledge) error
	UpdateKnowledge(context.Context, Knowledge) error
	QueryKnowledge(context.Context, KnowledgeQuery) ([]Knowledge, error)
}

// FinalizationRecord is the terminal audit write. The Run record, its verdict
// and the run.finalized event are committed together so the audit can never
// show a terminal Run without its verdict, or the reverse.
type FinalizationRecord struct {
	Run     RunRecord
	Verdict *VerdictRecord
	Event   Event
}

// Store is the canonical persistence port. It composes the four planes so a
// consumer can depend on the narrowest one it actually needs.
type Store interface {
	RunStore
	AuditStore
	ExplorationStore
	KnowledgeStore
	// Finalize commits the terminal record atomically and returns the stored
	// event with its assigned sequence.
	Finalize(context.Context, FinalizationRecord) (Event, error)
	Close() error
}

// EventSink persists raw session events.
type EventSink func(Event) error

// Logger is the minimal warning surface used by the core.
type Logger interface {
	Warn(msg string, args ...any)
}
