package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/pi"
	"github.com/red-copilot/harness/sandbox/docker"
	"github.com/red-copilot/harness/server"
	"github.com/red-copilot/harness/store/sqlite"
	"github.com/red-copilot/harness/verify"
	"github.com/red-copilot/harness/webui"
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
	case "serve":
		err = serve(args[1:], stdout, stderr)
	case "run":
		err = run(args[1:], stdout, stderr)
	case "runs":
		err = runsCommand(args[1:], stdout, stderr)
	case "doctor":
		err = doctor(args[1:], stdout, stderr)
	case "migrate":
		err = errors.New("legacy configuration is unsupported; migrate to a harness/v1 descriptor as described in docs/migration-v1.md")
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
  harness serve --config deployment.yaml [--db path] [--listen addr]
  harness run --config run.yaml [--db path] [--json]
  harness runs show|events [--db path] --id run-id
  harness doctor [--db path]
  harness version
The SDK enforces caller_managed target isolation; network ACLs are caller-owned.`)
}

func serve(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "deployment config")
	dbPath := fs.String("db", ".harness/harness.db", "SQLite database path")
	listen := fs.String("listen", "127.0.0.1:8080", "HTTP listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	base := &FileConfig{}
	if *configPath != "" {
		var err error
		base, err = LoadConfig(*configPath)
		if err != nil {
			return err
		}
	}
	store, err := sqlite.Open(*dbPath)
	if err != nil {
		return err
	}
	starter := docker.New()
	runner, err := harness.NewRunner(harness.Config{Store: store, Cleaner: starter, WorkspaceRoot: base.WorkspaceRoot})
	if err != nil {
		return err
	}
	builder := func(descriptor harness.RunDescriptor) (harness.RunSpec, error) {
		return composeDescriptor(base, descriptor, store)
	}
	handler := webui.Mount(server.New(runner, store, builder).Handler())
	fmt.Fprintln(stdout, "listening on", *listen)
	return http.ListenAndServe(*listen, handler)
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
	runner, err := harness.NewRunner(harness.Config{Store: store, Cleaner: starter, WorkspaceRoot: cfg.WorkspaceRoot})
	if err != nil {
		return err
	}
	result, runErr := runner.Run(context.Background(), spec)
	if *asJSON {
		_ = printJSON(stdout, result)
	} else {
		fmt.Fprintf(stdout, "%s %s score=%.0f\n", result.RunID, result.Status, scoreOf(result))
	}
	return runErr
}

func composeDescriptor(base *FileConfig, descriptor harness.RunDescriptor, store harness.ArtifactStore) (harness.RunSpec, error) {
	cfg := *base
	cfg.Task = descriptor.Task
	cfg.Target.ID = descriptor.Target.ID
	cfg.Target.Addresses = descriptor.Target.Addresses
	cfg.Target.Isolation = string(descriptor.Target.Isolation)
	if cfg.Target.Isolation == "" {
		cfg.Target.Isolation = string(harness.IsolationCallerManaged)
	}
	cfg.Planner = descriptor.Planner
	cfg.Executors = descriptor.Executors
	cfg.Policy = descriptor.Policy
	cfg.Budget = BudgetFile{WallTimeSeconds: int(descriptor.Budget.WallTime / time.Second), MaxTokens: descriptor.Budget.MaxTokens, MaxModelCalls: descriptor.Budget.MaxModelCalls, MaxToolCalls: descriptor.Budget.MaxToolCalls, MaxCostUSD: descriptor.Budget.MaxCostUSD}
	if cfg.Policy.MaxDepth == 0 {
		cfg.Policy = harness.DefaultExplorationPolicy()
	}
	return cfg.ToRunSpec(store)
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
	return printJSON(stdout, map[string]string{"status": "ok", "database": *dbPath, "isolation": string(harness.IsolationCallerManaged)})
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

func imageCommand(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("image build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	contextDir := fs.String("context", "images/pi-kali", "context")
	tag := fs.String("tag", pi.DefaultImage, "tag")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cmd := exec.Command("docker", "build", "-t", *tag, *contextDir)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}
func migrateCommand(taskPath, agentPath, outPath string) error {
	return errors.New("legacy migration requires docs/migration-v1.md mappings")
}

var _ = filepath.Clean
var _ = verify.Command{}
