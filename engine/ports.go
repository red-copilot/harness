package engine

import (
	"context"
	"io"
	"time"
)

// Clock is the only time source available to the kernel.
type Clock interface {
	Now() time.Time
}

// IDSource supplies opaque Run identities. Entity IDs remain board-local until
// the v5 domain cutover.
type IDSource interface {
	NewRunID() (string, error)
}

// SecretSource resolves secret-bearing environment values without letting the
// kernel read the process environment directly.
type SecretSource interface {
	LookupEnv(name string) (string, bool)
}

// RunDirs exposes the directory layout of one prepared Run workspace.
type RunDirs interface {
	Root() string
	TaskDir() string
	SkillsDir() string
	SessionDir(sessionID string) string
	PrepareSession(ctx context.Context, sessionID string) (string, error)
}

// EvidenceFile is one opened workspace file. Rel is the resolved path relative
// to the workspace root; the workspace has already confined it.
type EvidenceFile struct {
	Rel    string
	Size   int64
	Reader io.ReadCloser
}

// Workspace confines all filesystem interaction the kernel needs: run/session
// directories, the report protocol, evidence reads and verifier materialization.
type Workspace interface {
	PrepareRun(ctx context.Context, root, runID string, materializeSkills func(string) error) (RunDirs, error)
	ClearReport(ctx context.Context, workspaceDir string) error
	ReadReport(ctx context.Context, workspaceDir string, maxBytes int64) ([]byte, error)
	OpenEvidence(ctx context.Context, workspaceDir, relPath string, maxBytes int64) (EvidenceFile, error)
	MaterializeEvidence(ctx context.Context, root string, artifacts []Artifact, open func(context.Context, string) (io.ReadCloser, error)) (dir string, cleanup func() error, err error)
}

// AgentRuntimeFactory creates runtime-neutral, reset-capable sessions.
type AgentRuntimeFactory interface {
	NewSession(context.Context, RuntimeRequest) (AgentSession, error)
}

// AgentSession is one untrusted runtime process.
type AgentSession interface {
	Prompt(context.Context, string) error
	Reset(context.Context) error
	Events() <-chan AgentEvent
	Logs() <-chan LogEvent
	Usage() Usage
	Abort(context.Context) error
	Close(context.Context) error
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
	RunID            string
	ExpectedRevision uint64
	Revision         uint64
	Claims           []Claim
	Intents          []Intent
	Attempts         []IntentAttempt
	Observations     []Observation
	Artifacts        []Artifact
	Events           []Event
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

// BoardReader queries the append-only Claim/Intent board.
type BoardReader interface {
	ListClaims(context.Context, string) ([]Claim, error)
	ListIntents(context.Context, string) ([]Intent, error)
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
	OpenArtifact(context.Context, string) (io.ReadCloser, error)
	GetBoardRevision(context.Context, string) (uint64, error)
}

// BoardRestorer replays the newest canonical board commit for one Run. The
// restore path is read-only; the resumed engine re-enters through the
// canonical Commit boundary.
type BoardRestorer interface {
	RestoreBoardState(context.Context, string) (Commit, error)
}

// RunRecovery is the process-lifecycle and replay surface: it finalizes
// orphaned Runs and replays committed board revisions on resume.
type RunRecovery interface {
	BoardRestorer
	MarkInterrupted(context.Context) error
}

// Store composes the canonical writer, board/read queries, recovery and
// process lifecycle. Consumers that only read should depend on ReadStore, and
// components that only write Run lifecycle should depend on CanonicalWriter.
type Store interface {
	CanonicalWriter
	ReadStore
	RunRecovery
	Close() error
}

// EventSink persists raw session events.
type EventSink func(Event) error
