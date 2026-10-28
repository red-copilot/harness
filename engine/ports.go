package engine

import (
	"context"
	"io"
)

// AgentRuntimeFactory creates runtime-neutral, reset-capable sessions.
type AgentRuntimeFactory interface {
	NewSession(context.Context, RuntimeRequest) (AgentSession, error)
}

// AgentSession is one untrusted runtime process.
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

type ProcessStarter interface {
	Start(context.Context, ProcessSpec) (Process, error)
}

type ResourceCleaner interface {
	CleanupOrphans(context.Context) error
}

// TargetProvisioner creates and tears down the per-Run Target.
type TargetProvisioner interface {
	Provision(context.Context, ProvisionRequest) (TargetBinding, error)
	Close(context.Context, TargetBinding) error
}

// CompletionSource is an authoritative external completion signal.
type CompletionSource interface {
	Observe(context.Context, TargetBinding) (Completion, error)
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

// Commit is one atomic canonical board/audit transition.
type Commit struct {
	RunID        string
	Revision     uint64
	Facts        []Fact
	Intents      []Intent
	Hints        []Hint
	Attempts     []IntentAttempt
	Observations []Observation
	Artifacts    []Artifact
	Events       []Event
}

// FinalizationRecord is the atomic terminal audit write.
type FinalizationRecord struct {
	Run     RunRecord
	Verdict *VerdictRecord
	Event   Event
}

// CanonicalWriter is the only write boundary for Run and board state.
type CanonicalWriter interface {
	CreateRun(context.Context, RunRecord) error
	UpdateRun(context.Context, RunRecord) error
	AppendEvent(context.Context, Event) (Event, error)
	AppendLog(context.Context, LogRecord) (LogRecord, error)
	Commit(context.Context, Commit) ([]Event, error)
	Finalize(context.Context, FinalizationRecord) (Event, error)
}

// BoardReader queries the append-only Fact/Intent board.
type BoardReader interface {
	ListFacts(context.Context, string) ([]Fact, error)
	ListIntents(context.Context, string) ([]Intent, error)
	ListHints(context.Context, string) ([]Hint, error)
	ListIntentAttempts(context.Context, string) ([]IntentAttempt, error)
	ListBlackboardObservations(context.Context, string) ([]Observation, error)
}

// ReadStore is the query boundary.
type ReadStore interface {
	BoardReader
	GetRun(context.Context, string) (RunRecord, error)
	ListRuns(context.Context, int) ([]RunRecord, error)
	ListEvents(context.Context, string, int64) ([]Event, error)
	ListLogs(context.Context, string, int64) ([]LogRecord, error)
	GetVerdict(context.Context, string) (VerdictRecord, error)
	ListArtifacts(context.Context, string) ([]Artifact, error)
	GetBoardRevision(context.Context, string) (uint64, error)
}

// Store composes the canonical writer, board reads and process lifecycle.
type Store interface {
	CanonicalWriter
	ReadStore
	MarkInterrupted(context.Context) error
	Close() error
}

// EventSink persists raw session events.
type EventSink func(Event) error
