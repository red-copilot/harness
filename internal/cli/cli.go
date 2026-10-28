package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/sandbox/docker"
	"github.com/red-copilot/harness/server"
	"github.com/red-copilot/harness/store/sqlite"
	"github.com/red-copilot/harness/tsec"
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
	case "tsec":
		err = tsecCommand(args[1:], stdout, stderr)
	case "runs":
		err = runsCommand(args[1:], stdout, stderr)
	case "doctor":
		err = doctor(args[1:], stdout, stderr)
	case "backup":
		err = backup(args[1:], stdout, stderr)
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
  harness tsec sweep --config tsec.yaml [--db path] [--parallel n]
  harness runs show|events [--db path] --id run-id
  harness doctor [--db path]
  harness backup --db path --out path [--manifest path]
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
	runner, err := harness.NewRunner(harness.Config{Store: store, Starter: starter, Cleaner: starter, WorkspaceRoot: base.WorkspaceRoot, CloseTimeout: 30 * time.Second})
	if err != nil {
		return err
	}
	builder := func(descriptor harness.RunDescriptor) (harness.RunSpec, error) {
		return composeDescriptor(base, descriptor, store)
	}
	sweepBuilder := func(request server.SweepRequest) (server.SweepPlan, error) {
		return composeSweepPlan(base, request, store, *dbPath)
	}
	handler := webui.Mount(server.New(runner, store, builder, sweepBuilder).Handler())
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
	runner, err := harness.NewRunner(harness.Config{Store: store, Starter: starter, Cleaner: starter, WorkspaceRoot: cfg.WorkspaceRoot, CloseTimeout: 30 * time.Second})
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

func tsecCommand(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "sweep" {
		return errors.New("tsec requires sweep")
	}
	fs := flag.NewFlagSet("tsec sweep", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "v2 TSec descriptor config")
	dbPath := fs.String("db", ".harness/harness.db", "SQLite database path")
	parallel := fs.Int("parallel", 0, "parallel challenges (1-3)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *configPath == "" {
		return errors.New("config path is required")
	}
	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return err
	}
	if cfg.TSec == nil {
		return errors.New("tsec configuration is required for a sweep")
	}
	store, err := sqlite.Open(*dbPath)
	if err != nil {
		return err
	}
	descriptor, err := cfg.Descriptor()
	if err != nil {
		store.Close()
		return err
	}
	request := server.SweepRequest{Descriptor: descriptor, TSec: &server.TSecRequest{
		BaseURL: cfg.TSec.BaseURL, Token: cfg.TSec.Token,
	}, Challenges: []string{cfg.TSec.Challenge}, Parallel: *parallel, UseHint: cfg.TSec.UseHint}
	plan, err := composeSweepPlan(cfg, request, store, *dbPath)
	if err != nil {
		store.Close()
		return err
	}
	starter := docker.New()
	runner, err := harness.NewRunner(harness.Config{Store: store, Starter: starter, Cleaner: starter, WorkspaceRoot: cfg.WorkspaceRoot, CloseTimeout: 30 * time.Second})
	if err != nil {
		store.Close()
		return err
	}
	defer runner.Close(context.Background())
	if plan.Parallel > 3 {
		plan.Parallel = 3
	}
	if plan.Parallel <= 0 {
		plan.Parallel = 3
	}
	type outcome struct {
		Code   string         `json:"code"`
		Result harness.Result `json:"result,omitempty"`
		Error  string         `json:"error,omitempty"`
	}
	outcomes := make([]outcome, len(plan.ChallengeCodes))
	sem := make(chan struct{}, plan.Parallel)
	done := make(chan int, len(plan.ChallengeCodes))
	for index, code := range plan.ChallengeCodes {
		go func(index int, code string) {
			sem <- struct{}{}
			defer func() { <-sem }()
			spec, buildErr := plan.Build(code)
			if buildErr != nil {
				outcomes[index] = outcome{Code: code, Error: buildErr.Error()}
				done <- index
				return
			}
			result, runErr := runner.Run(context.Background(), spec)
			outcomes[index] = outcome{Code: code, Result: result}
			if runErr != nil {
				outcomes[index].Error = runErr.Error()
			}
			done <- index
		}(index, code)
	}
	for range plan.ChallengeCodes {
		<-done
	}
	return printJSON(stdout, outcomes)
}

func composeSweepPlan(base *FileConfig, request server.SweepRequest, store harness.ArtifactStore, dbPath string) (server.SweepPlan, error) {
	if request.TSec == nil {
		return server.SweepPlan{}, errors.New("tsec configuration is required for a sweep")
	}
	if err := harness.ValidateDescriptor(request.Descriptor); err != nil {
		return server.SweepPlan{}, err
	}
	baseURL := request.TSec.BaseURL
	if baseURL == "" && base.TSec != nil {
		baseURL = os.Getenv(base.TSec.BaseURLEnv)
	}
	if baseURL == "" {
		baseURL = os.Getenv("BENCHMARK_BASE_URL")
	}
	token := request.TSec.Token
	if token == "" && base.TSec != nil {
		token = os.Getenv(base.TSec.TokenEnv)
	}
	if token == "" {
		token = os.Getenv("BENCHMARK_TOKEN")
	}
	client, err := tsec.New(tsec.Config{BaseURL: baseURL, Token: token, CheckVPN: true})
	if err != nil {
		return server.SweepPlan{}, err
	}
	challenges, err := client.ListChallenges(context.Background())
	if err != nil {
		return server.SweepPlan{}, err
	}
	byCode := make(map[string]tsec.Challenge, len(challenges))
	for _, challenge := range challenges {
		byCode[challenge.UniqueCode] = challenge
	}
	codes := append([]string(nil), request.Challenges...)
	if len(codes) == 0 {
		for _, challenge := range challenges {
			if !challenge.IsCompleted {
				codes = append(codes, challenge.UniqueCode)
			}
		}
	}
	if len(codes) == 0 {
		return server.SweepPlan{}, errors.New("no challenges selected")
	}
	useHint := request.UseHint
	if base.TSec != nil {
		useHint = useHint || base.TSec.UseHint
	}
	return server.SweepPlan{
		ChallengeCodes: codes,
		Parallel:       request.Parallel,
		Build: func(code string) (harness.RunSpec, error) {
			challenge, ok := byCode[code]
			if !ok {
				return harness.RunSpec{}, fmt.Errorf("unknown challenge %q", code)
			}
			descriptor := request.Descriptor
			descriptor.Task.ID = "tsec-" + code
			descriptor.Task.Objective = tsecObjective(challenge)
			descriptor.Target.ID = "tsec-" + code
			descriptor.Target.Addresses = append([]string(nil), challenge.ContainerAddr...)
			cfg := *base
			cfg.Task = descriptor.Task
			cfg.Target = targetFromDescriptor(descriptor)
			cfg.Target.Kind = "tsec"
			cfg.Target.ID = descriptor.Target.ID
			cfg.Target.Addresses = descriptor.Target.Addresses
			cfg.Target.Isolation = string(descriptor.Target.Isolation)
			if cfg.Target.Isolation == "" {
				cfg.Target.Isolation = string(harness.IsolationCallerManaged)
			}
			cfg.Reasoner = agentFromHarness(descriptor.Reasoner)
			cfg.Executors = make([]AgentFileSpec, 0, len(descriptor.Executors))
			for _, executor := range descriptor.Executors {
				cfg.Executors = append(cfg.Executors, agentFromHarness(executor))
			}
			cfg.Policy = policyFileFromRunPolicy(descriptor.Policy)
			if cfg.TSec == nil {
				cfg.TSec = &TSecFileConfig{}
			}
			cfg.TSec.Challenge = code
			cfg.TSec.UseHint = useHint
			spec, err := cfg.ToRunSpec(store)
			if err != nil {
				return harness.RunSpec{}, err
			}
			spec.Policy = harness.NormalizeRunPolicy(descriptor.Policy)
			return spec, nil
		},
	}, nil
}

func policyFileFromRunPolicy(policy harness.RunPolicy) PolicyFile {
	e := harness.NormalizeRunPolicy(policy).Exploration
	return PolicyFile{
		MaxOpenIntents: e.MaxOpenIntents, MaxTotalIntents: e.MaxTotalIntents,
		MaxAttemptsPerIntent: e.MaxAttemptsPerIntent, MaxToolCallsPerAttempt: e.MaxToolCallsPerAttempt,
		MaxWallTimePerAttempt: e.MaxWallTimePerAttempt, ConcludeTimeout: e.ConcludeTimeout,
		StagnationRounds: e.StagnationRounds, MaxReasonerToolCalls: e.MaxReasonerToolCalls,
		MaxReasonerWallTime: e.MaxReasonerWallTime, MaxReasonerFailures: e.MaxReasonerFailures,
		BootstrapEnabled: e.BootstrapEnabled,
	}
}

func targetFromDescriptor(descriptor harness.RunDescriptor) TargetFileConfig {
	return TargetFileConfig{ID: descriptor.Target.ID, Addresses: append([]string(nil), descriptor.Target.Addresses...), Isolation: string(descriptor.Target.Isolation)}
}

func tsecObjective(challenge tsec.Challenge) string {
	description := challenge.Description
	if description == "" {
		description = "No platform description was provided."
	}
	return fmt.Sprintf("Authorized TSec challenge %s.\n\n%s\n\nFind and submit every remaining flag for this challenge.", challenge.UniqueCode, description)
}

func composeDescriptor(base *FileConfig, descriptor harness.RunDescriptor, store harness.ArtifactStore) (harness.RunSpec, error) {
	if err := harness.ValidateDescriptor(descriptor); err != nil {
		return harness.RunSpec{}, err
	}
	cfg := *base
	cfg.Task = descriptor.Task
	cfg.Target.ID = descriptor.Target.ID
	cfg.Target.Addresses = descriptor.Target.Addresses
	cfg.Target.Isolation = string(descriptor.Target.Isolation)
	if cfg.Target.Isolation == "" {
		cfg.Target.Isolation = string(harness.IsolationCallerManaged)
	}
	cfg.Reasoner = agentFromHarness(descriptor.Reasoner)
	cfg.Executors = make([]AgentFileSpec, 0, len(descriptor.Executors))
	for _, executor := range descriptor.Executors {
		cfg.Executors = append(cfg.Executors, agentFromHarness(executor))
	}
	spec, err := cfg.ToRunSpec(store)
	if err != nil {
		return harness.RunSpec{}, err
	}
	spec.Policy = harness.NormalizeRunPolicy(descriptor.Policy)
	return spec, nil
}

func agentFromHarness(agent harness.AgentDescriptor) AgentFileSpec {
	converted := AgentFileSpec{ID: agent.ID, Image: agent.Image, Command: agent.Command, Env: agent.Env, EnvFrom: agent.EnvFrom, Mounts: agent.Mounts, Network: agent.Network, CapAdd: agent.CapAdd, Resources: agent.Resources}
	if len(agent.RuntimeConfig) > 0 {
		var runtimeConfig any
		if json.Unmarshal(agent.RuntimeConfig, &runtimeConfig) == nil {
			converted.RuntimeConfig = runtimeConfig
		}
	}
	return converted
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
