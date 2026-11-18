package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

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
	cancelled, err := store.MarkRunInterrupted(context.Background(), *id)
	if err != nil {
		return err
	}
	if !cancelled {
		return fmt.Errorf("run %s is unknown or already terminal", *id)
	}
	fmt.Fprintf(stdout, "%s cancelled\n", *id)
	return nil
}
