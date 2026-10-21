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

type Backend interface {
	Provision(context.Context, ProvisionRequest) (Target, error)
	Observe(context.Context, Target) (Completion, error)
	Close(context.Context, Target) error
}

type Verifier interface {
	Verify(context.Context, VerifyRequest) (Verdict, error)
}

type ArtifactStore interface {
	Put(context.Context, ArtifactInput) (Artifact, error)
	Open(context.Context, string) (io.ReadCloser, error)
}

type Store interface {
	CreateRun(context.Context, RunRecord) error
	UpdateRun(context.Context, RunRecord) error
	GetRun(context.Context, string) (RunRecord, error)
	ListRuns(context.Context, int) ([]RunRecord, error)

	AppendEvent(context.Context, Event) (Event, error)
	ListEvents(context.Context, string, int64) ([]Event, error)
	AppendLog(context.Context, LogRecord) (LogRecord, error)
	ListLogs(context.Context, string, int64) ([]LogRecord, error)

	SaveVerdict(context.Context, VerdictRecord) error
	GetVerdict(context.Context, string) (VerdictRecord, error)

	SaveBranch(context.Context, Branch) error
	ListBranches(context.Context, string) ([]Branch, error)
	SaveAttempt(context.Context, Attempt) error
	ListAttempts(context.Context, string) ([]Attempt, error)
	SaveObservation(context.Context, Observation) error
	ListObservations(context.Context, string) ([]Observation, error)
	SaveArtifact(context.Context, Artifact) error
	ListArtifacts(context.Context, string) ([]Artifact, error)

	SaveKnowledge(context.Context, Knowledge) error
	UpdateKnowledge(context.Context, Knowledge) error
	QueryKnowledge(context.Context, KnowledgeQuery) ([]Knowledge, error)

	SaveSweep(context.Context, SweepRecord) error
	GetSweep(context.Context, string) (SweepRecord, error)
	ListSweeps(context.Context, int) ([]SweepRecord, error)
	MarkInterrupted(context.Context) error
	Close() error
}

// EventSink persists raw session events.
type EventSink func(Event) error

// Logger is the minimal warning surface used by the core.
type Logger interface {
	Warn(msg string, args ...any)
}
