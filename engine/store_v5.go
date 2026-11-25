package engine

import (
	"context"
	"io"

	"github.com/red-copilot/harness/core"
)

// The v5 persistence ports are consumer-owned and deliberately narrow. They
// replace the v4 Journal/Reader/Recovery union during P4. The V5 prefix is
// temporary: once the v4 interfaces are deleted these become the kernel's
// Journal, Reader, Recovery and ArtifactWriter.

// V5Journal is the only write boundary for the v5 journal and its state
// projection. Every write is atomic and extends the expected journal head.
type V5Journal interface {
	Create(ctx context.Context, manifest core.SealedManifest, first core.SealedBatch) (core.State, error)
	Append(ctx context.Context, batch core.SealedBatch) (core.AppendResult, error)
	Finalize(ctx context.Context, finalization core.SealedFinalization) (core.AppendResult, error)
}

// V5Reader is the read surface the v5 kernel needs. Read-model aggregates and
// listing commands stay outside the kernel contract.
type V5Reader interface {
	State(ctx context.Context, runID string) (core.State, error)
	Manifest(ctx context.Context, runID string) (core.Manifest, error)
	Events(ctx context.Context, runID string, after uint64) ([]core.Record, error)
	Logs(ctx context.Context, runID string, after uint64) ([]core.LogRecord, error)
	Verdict(ctx context.Context, runID string) (*core.Verdict, error)
	Board(ctx context.Context, runID string) (core.Board, error)
	ListArtifacts(ctx context.Context, runID string) ([]core.Artifact, error)
	OpenArtifact(ctx context.Context, sha256 string) (io.ReadCloser, error)
	VerifyChain(ctx context.Context, runID string) error
}

// V5Recovery is the process-lifecycle and replay surface.
type V5Recovery interface {
	NonTerminal(ctx context.Context) ([]core.State, error)
	Replay(ctx context.Context, runID string) (core.Restored, error)
	Interrupt(ctx context.Context, finalization core.SealedFinalization) (core.State, error)
}

// V5ArtifactWriter stores content-addressed, already-sealed evidence bytes.
type V5ArtifactWriter interface {
	Put(ctx context.Context, artifact core.SealedArtifact) (core.Artifact, error)
}

// V5LogWriter appends already-redacted log records outside the hash chain.
type V5LogWriter interface {
	AppendLogs(ctx context.Context, runID string, logs []core.SealedLog) error
}

// V5Store is the composite the v5 kernel and the conformance suite require.
type V5Store interface {
	V5Journal
	V5Reader
	V5Recovery
	V5ArtifactWriter
	V5LogWriter
	StoreCloser
}
