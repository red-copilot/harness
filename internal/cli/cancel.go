package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/red-copilot/harness/core"
	"github.com/red-copilot/harness/store/sqlite"
)

// runsCancel implements `harness runs cancel`. A Run is only cancelled when no
// live process holds the recovery lock: the command acquires the lock itself,
// so success proves the Run has no owner and is safe to finalize as
// interrupted. While a runner is alive the command refuses instead of racing
// it — an owned Run must be stopped through its own context.
func runsCancel(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("runs cancel", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", ".harness/harness.db", "database")
	id := fs.String("id", "", "run id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return errors.New("run id is required")
	}
	lock, err := recoveryGuard(*dbPath)
	if err != nil {
		return fmt.Errorf("run %s has a live owner and cannot be cancelled from another process: %w", *id, err)
	}
	defer func() { _ = lock() }()
	store, err := sqlite.Open(*dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	ctx := context.Background()
	bridge := sqlite.NewV4Bridge(store)
	state, err := bridge.State(ctx, *id)
	switch {
	case err == nil:
		if state.Phase == core.PhaseTerminal {
			return fmt.Errorf("run %s is already terminal", *id)
		}
		if err := interruptBridgeRun(ctx, bridge, state); err != nil {
			return err
		}
	case errors.Is(err, sqlite.ErrNotBridgeRun):
		cancelled, fallbackErr := store.MarkRunInterrupted(ctx, *id)
		if fallbackErr != nil {
			return fallbackErr
		}
		if !cancelled {
			return fmt.Errorf("run %s is unknown or already terminal", *id)
		}
	default:
		return err
	}
	fmt.Fprintf(stdout, "%s cancelled\n", *id)
	return nil
}

// interruptBridgeRun finalizes a v5 bridge Run through the same sealed,
// expected-head terminal path the kernel uses.
func interruptBridgeRun(ctx context.Context, bridge *sqlite.V4Bridge, state core.State) error {
	now := time.Now().UTC()
	record, err := core.NewRecord(state.RunID, state.HeadSeq+1, core.RecordRunInterrupted, now, core.RunInterruptedRecord{
		Reason: "operator cancel", Operator: "cli", At: now,
	}, state.HeadHash)
	if err != nil {
		return err
	}
	sealed, err := core.NewRedactor().SealFinalization(core.Finalization{
		RunID: state.RunID, ExpectedHead: state.HeadSeq, Outcome: core.OutcomeInterrupted,
		Error: "operator cancel", Records: []core.Record{record}, FinishedAt: now,
	})
	if err != nil {
		return err
	}
	if _, err := bridge.Interrupt(ctx, sealed); err != nil {
		if errors.Is(err, core.ErrStaleHead) || errors.Is(err, core.ErrTerminal) {
			return fmt.Errorf("run %s changed while cancelling; retry after confirming no live owner: %w", state.RunID, err)
		}
		return err
	}
	return nil
}
