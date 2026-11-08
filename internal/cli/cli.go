package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/sandbox/docker"
	"github.com/red-copilot/harness/store/sqlite"
)

const Version = "0.1.0"

func Main(args []string, stdout, stderr io.Writer) int {
	if err := loadEnvFile(".env"); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if len(args) == 0 {
		printUsage(stdout)
		return 0
	}
	var err error
	switch args[0] {
	case "run":
		err = run(args[1:], stdout, stderr)
	case "runs":
		err = runsCommand(args[1:], stdout, stderr)
	case "doctor":
		err = doctor(args[1:], stdout, stderr)
	case "backup":
		err = backup(args[1:], stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, Version)
	case "help", "--help", "-h":
		printUsage(stdout)
	default:
		err = fmt.Errorf("unknown command %q", args[0])
	}
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `Red Harness Go SDK
Usage:
  harness run --config run.yaml [--db path] [--json]
  harness runs show|events [--db path] --id run-id
  harness doctor [--db path]
  harness backup --db path --out path [--manifest path]
  harness version
The SDK enforces caller_managed target isolation; network ACLs are caller-owned.`)
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "run config")
	filePath := fs.String("file", "", "run config alias")
	dbPath := fs.String("db", ".harness/harness.db", "SQLite database path")
	asJSON := fs.Bool("json", false, "print JSON result")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		*configPath = *filePath
	}
	if *configPath == "" {
		return errors.New("config path is required")
	}
	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return err
	}
	store, err := sqlite.Open(*dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	spec, err := cfg.ToRunSpec(store)
	if err != nil {
		return err
	}
	starter := docker.New()
	runner, err := harness.NewRunner(harness.Config{Store: store, Starter: starter, Cleaner: starter, WorkspaceRoot: cfg.WorkspaceRoot, CloseTimeout: 30 * time.Second})
	if err != nil {
		return err
	}
	result, runErr := runner.Run(context.Background(), spec)
	if *asJSON {
		_ = printJSON(stdout, result)
	} else {
		fmt.Fprintf(stdout, "%s %s/%s score=%.0f\n", result.RunID, result.Phase, result.Outcome, scoreOf(result))
	}
	return runErr
}

func runsCommand(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("runs requires show or events")
	}
	switch args[0] {
	case "show":
		fs := flag.NewFlagSet("runs show", flag.ContinueOnError)
		fs.SetOutput(stderr)
		dbPath := fs.String("db", ".harness/harness.db", "database")
		id := fs.String("id", "", "run id")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *id == "" {
			return errors.New("run id is required")
		}
		store, err := sqlite.Open(*dbPath)
		if err != nil {
			return err
		}
		defer store.Close()
		record, err := store.GetRun(context.Background(), *id)
		if err != nil {
			return err
		}
		return printJSON(stdout, record)
	case "events":
		fs := flag.NewFlagSet("runs events", flag.ContinueOnError)
		fs.SetOutput(stderr)
		dbPath := fs.String("db", ".harness/harness.db", "database")
		id := fs.String("id", "", "run id")
		after := fs.Int64("after", 0, "after sequence")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *id == "" {
			return errors.New("run id is required")
		}
		store, err := sqlite.Open(*dbPath)
		if err != nil {
			return err
		}
		defer store.Close()
		events, err := store.ListEvents(context.Background(), *id, *after)
		if err != nil {
			return err
		}
		for _, event := range events {
			if err := printJSONLine(stdout, event); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown runs command %q", args[0])
	}
}

func doctor(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", ".harness/harness.db", "database")
	if err := fs.Parse(args); err != nil {
		return err
	}
	store, err := sqlite.Open(*dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.VerifyEventChain(context.Background()); err != nil {
		return fmt.Errorf("verify audit chain: %w", err)
	}
	return printJSON(stdout, map[string]string{"status": "ok", "database": *dbPath, "schema": "v8", "isolation": string(harness.IsolationCallerManaged)})
}

func backup(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", ".harness/harness.db", "source SQLite database")
	outPath := fs.String("out", "", "backup database path")
	manifestPath := fs.String("manifest", "", "manifest path (defaults to <out>.json)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *outPath == "" {
		return errors.New("--out is required")
	}
	if *manifestPath == "" {
		*manifestPath = *outPath + ".json"
	}
	manifest, err := sqlite.CreateBackup(*dbPath, *outPath, *manifestPath)
	if err != nil {
		return err
	}
	return printJSON(stdout, manifest)
}

func scoreOf(result harness.Result) float64 {
	if result.Verdict == nil {
		return 0
	}
	return result.Verdict.Score
}
func printJSON(w io.Writer, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}
func printJSONLine(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}
