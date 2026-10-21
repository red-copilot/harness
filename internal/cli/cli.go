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
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/pi"
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
	case "image":
		err = imageCommand(args[1:], stdout, stderr)
	case "doctor":
		err = doctor(args[1:], stdout, stderr)
	case "migrate":
		err = migrateCommand(args[1:], stdout, stderr)
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
  harness serve [flags]
  harness run --file run.yaml [flags]
  harness tsec list [flags]
  harness tsec run [flags]
  harness runs show|events|cancel [flags]
  harness image build [flags]
  harness doctor [flags]
  harness migrate --task task.yaml --agent agent.yaml --out run.json
  harness version`)
}

func serve(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", ".harness/harness.db", "SQLite database path")
	listen := fs.String("listen", "127.0.0.1:8080", "HTTP listen address")
	workspace := fs.String("workspace", ".harness/workspaces", "workspace root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	store, err := sqlite.Open(*dbPath)
	if err != nil {
		return err
	}
	starter := docker.New()
	runner, err := harness.NewRunner(harness.Config{
		Store: store, Starter: starter, DefaultFactory: pi.NewFactory(),
		WorkspaceRoot: *workspace, Cleaner: starter, SkipRecovery: true,
	})
	if err != nil {
		return err
	}
	builder := func(req server.RunRequest) (harness.RunSpec, error) {
		spec := req.Run
		applyPiEnvironmentDefaults(&spec)
		if req.VerifierCommand != nil {
			spec.Verifier = *req.VerifierCommand
		}
		if req.Target != nil && req.TSec == nil {
			switch req.Target.Kind {
			case "", "external":
				spec.Target = harness.Target{ID: req.Target.ID, Addresses: req.Target.Addresses}
			case "image":
				spec.Backend = docker.ImageBackend{
					Image: req.Target.Image, Command: req.Target.Command, Ports: req.Target.Ports,
					Network: req.Target.Network, Addresses: req.Target.Addresses,
				}
			case "compose":
				spec.Backend = docker.ComposeBackend{
					File: req.Target.ComposeFile, ProjectName: req.Target.ProjectName,
					Service: req.Target.Service, Port: req.Target.Port, Addresses: req.Target.Addresses,
				}
			default:
				return harness.RunSpec{}, fmt.Errorf("unsupported target kind %q", req.Target.Kind)
			}
		}
		if req.TSec != nil {
			baseURL := req.TSec.BaseURL
			if baseURL == "" {
				baseURL = os.Getenv("BENCHMARK_BASE_URL")
			}
			token := req.TSec.Token
			if token == "" {
				token = os.Getenv("BENCHMARK_TOKEN")
			}
			client, err := tsec.New(tsec.Config{BaseURL: baseURL, Token: token, CheckVPN: true})
			if err != nil {
				return harness.RunSpec{}, err
			}
			applyTSec(&spec, client, req.TSec.Challenge, req.TSec.UseHint)
		}
		return spec, nil
	}
	sweepBuilder := func(req server.SweepRequest) (server.SweepPlan, error) {
		if req.TSec == nil {
			return server.SweepPlan{}, errors.New("tsec configuration is required for a sweep")
		}
		applyPiEnvironmentDefaults(&req.Run)
		baseURL := req.TSec.BaseURL
		if baseURL == "" {
			baseURL = os.Getenv("BENCHMARK_BASE_URL")
		}
		token := req.TSec.Token
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
		codes := req.Challenges
		if len(codes) == 0 {
			for _, challenge := range challenges {
				if !challenge.IsCompleted {
					codes = append(codes, challenge.UniqueCode)
				}
			}
		}
		descriptions := map[string]string{}
		for _, challenge := range challenges {
			descriptions[challenge.UniqueCode] = challenge.Description
		}
		return server.SweepPlan{
			ChallengeCodes: codes, Parallel: req.Parallel,
			Build: func(code string) (harness.RunSpec, error) {
				spec := req.Run
				spec.Agents = append([]harness.AgentSpec(nil), req.Run.Agents...)
				applyTSec(&spec, client, code, req.UseHint)
				spec.Task.ID = "tsec-" + code
				spec.Task.Objective = fmt.Sprintf("Authorized TSec challenge %s.\n\n%s\n\nFind and submit every remaining flag.", code, descriptions[code])
				return spec, nil
			},
		}, nil
	}
	handler := webui.Mount(server.New(runner, store, builder, sweepBuilder).Handler())
	fmt.Fprintf(stdout, "listening on http://%s\n", *listen)
	return http.ListenAndServe(*listen, handler)
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("file", "", "run YAML/JSON file")
	dbPath := fs.String("db", ".harness/harness.db", "SQLite database path")
	workspace := fs.String("workspace", "", "workspace root")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return errors.New("--file is required")
	}
	cfg, err := LoadConfig(*file)
	if err != nil {
		return err
	}
	spec, err := cfg.ToRunSpec()
	if err != nil {
		return err
	}
	applyPiEnvironmentDefaults(&spec)
	if *workspace != "" {
		spec.WorkspaceRoot = *workspace
	}
	runner, _, err := newRunner(*dbPath, spec.WorkspaceRoot)
	if err != nil {
		return err
	}
	defer runner.Close(context.Background())
	if cfg.TSec != nil {
		client, err := clientFromTSec(cfg.TSec, "")
		if err != nil {
			return err
		}
		if cfg.TSec.All {
			return runSweep(context.Background(), runner, client, spec, cfg.TSec, stdout, *asJSON)
		}
		challenge := cfg.TSec.Challenge
		if challenge == "" {
			return errors.New("tsec.challenge or tsec.all is required")
		}
		applyTSec(&spec, client, challenge, cfg.TSec.UseHint)
	}
	result, err := runner.Run(context.Background(), spec)
	printResult(stdout, result, *asJSON)
	if err != nil {
		return err
	}
	if result.Status != harness.StatusSucceeded {
		return fmt.Errorf("run finished with status %s", result.Status)
	}
	return nil
}

func tsecCommand(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("tsec requires list or run")
	}
	switch args[0] {
	case "list":
		return tsecList(args[1:], stdout, stderr)
	case "run":
		return tsecRun(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown tsec command %q", args[0])
	}
}

func tsecList(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("tsec list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	baseURL := fs.String("base-url", "", "benchmark base URL")
	token := fs.String("token", "", "benchmark token")
	baseEnv := fs.String("base-url-env", "BENCHMARK_BASE_URL", "base URL environment variable")
	tokenEnv := fs.String("token-env", "BENCHMARK_TOKEN", "token environment variable")
	noVPN := fs.Bool("no-vpn", false, "skip VPN check")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := tsecClient(*baseURL, *token, *baseEnv, *tokenEnv, !*noVPN)
	if err != nil {
		return err
	}
	if !*noVPN {
		if _, err := client.CheckVPN(context.Background()); err != nil {
			return err
		}
	}
	challenges, err := client.ListChallenges(context.Background())
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(stdout, challenges)
	}
	for _, challenge := range challenges {
		fmt.Fprintf(stdout, "%-24s %d/%d flags  %-10s %s\n", challenge.UniqueCode,
			challenge.CorrectFlagCount, challenge.FlagCount, challenge.ContainerStatus, challenge.Description)
	}
	return nil
}

func tsecRun(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("tsec run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("file", "", "run YAML/JSON file")
	challenge := fs.String("challenge", "", "challenge unique_code")
	all := fs.Bool("all", false, "run all unfinished challenges")
	parallel := fs.Int("parallel", 3, "parallel challenges")
	hint := fs.Bool("hint", false, "request hint (reduces score)")
	dbPath := fs.String("db", ".harness/harness.db", "SQLite database path")
	workspace := fs.String("workspace", "", "workspace root")
	baseURL := fs.String("base-url", "", "benchmark base URL")
	token := fs.String("token", "", "benchmark token")
	baseEnv := fs.String("base-url-env", "BENCHMARK_BASE_URL", "base URL environment variable")
	tokenEnv := fs.String("token-env", "BENCHMARK_TOKEN", "token environment variable")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return errors.New("--file is required")
	}
	cfg, err := LoadConfig(*file)
	if err != nil {
		return err
	}
	spec, err := cfg.ToRunSpec()
	if err != nil {
		return err
	}
	applyPiEnvironmentDefaults(&spec)
	if *workspace != "" {
		spec.WorkspaceRoot = *workspace
	}
	runner, _, err := newRunner(*dbPath, spec.WorkspaceRoot)
	if err != nil {
		return err
	}
	defer runner.Close(context.Background())
	client, err := tsecClient(*baseURL, *token, *baseEnv, *tokenEnv, true)
	if err != nil {
		return err
	}
	tsecCfg := &TSecFileConfig{Challenge: *challenge, All: *all, Parallel: *parallel, UseHint: *hint}
	if *all {
		return runSweep(context.Background(), runner, client, spec, tsecCfg, stdout, *asJSON)
	}
	if *challenge == "" {
		return errors.New("--challenge or --all is required")
	}
	applyTSec(&spec, client, *challenge, *hint)
	result, err := runner.Run(context.Background(), spec)
	printResult(stdout, result, *asJSON)
	if err != nil {
		return err
	}
	if result.Status != harness.StatusSucceeded {
		return fmt.Errorf("run finished with status %s", result.Status)
	}
	return nil
}

func runSweep(ctx context.Context, runner *harness.Runner, client *tsec.Client, base harness.RunSpec, cfg *TSecFileConfig, stdout io.Writer, asJSON bool) error {
	challenges, err := client.ListChallenges(ctx)
	if err != nil {
		return err
	}
	parallel := cfg.Parallel
	if parallel <= 0 || parallel > 3 {
		parallel = 3
	}
	type job struct{ challenge tsec.Challenge }
	jobs := make(chan job)
	var wg sync.WaitGroup
	var mu sync.Mutex
	results := []harness.Result{}
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				spec := base
				spec.Agents = append([]harness.AgentSpec(nil), base.Agents...)
				applyTSec(&spec, client, item.challenge.UniqueCode, cfg.UseHint)
				spec.Task.ID = "tsec-" + item.challenge.UniqueCode
				spec.Task.Objective = tsecObjective(item.challenge)
				result, _ := runner.Run(ctx, spec)
				mu.Lock()
				results = append(results, result)
				mu.Unlock()
			}
		}()
	}
	for _, challenge := range challenges {
		if challenge.IsCompleted {
			continue
		}
		jobs <- job{challenge: challenge}
	}
	close(jobs)
	wg.Wait()
	if asJSON {
		return printJSON(stdout, results)
	}
	for _, result := range results {
		fmt.Fprintf(stdout, "%s %s score=%.0f\n", result.RunID, result.Status, scoreOf(result))
	}
	return nil
}

func applyTSec(spec *harness.RunSpec, client *tsec.Client, code string, useHint bool) {
	backend := tsec.NewBackend(client, code)
	backend.UseHint = useHint
	spec.Backend = backend
	if spec.Task.Objective == "" {
		spec.Task.Objective = "Authorized TSec challenge " + code + ". Find and submit every remaining flag."
	}
	for i := range spec.Agents {
		spec.Agents[i].EnvFrom = appendUnique(spec.Agents[i].EnvFrom, "BENCHMARK_BASE_URL")
		spec.Agents[i].EnvFrom = appendUnique(spec.Agents[i].EnvFrom, "BENCHMARK_TOKEN")
	}
	spec.SecretEnv = appendUnique(spec.SecretEnv, "BENCHMARK_TOKEN")
}

func newRunner(dbPath, workspace string) (*harness.Runner, harness.Store, error) {
	store, err := sqlite.Open(dbPath)
	if err != nil {
		return nil, nil, err
	}
	starter := docker.New()
	if workspace == "" {
		workspace = ".harness/workspaces"
	}
	runner, err := harness.NewRunner(harness.Config{
		Store: store, Starter: starter, DefaultFactory: pi.NewFactory(),
		WorkspaceRoot: workspace, Cleaner: starter,
	})
	if err != nil {
		return nil, nil, err
	}
	return runner, store, nil
}

func tsecClient(baseURL, token, baseEnv, tokenEnv string, checkVPN bool) (*tsec.Client, error) {
	if baseURL == "" {
		baseURL = os.Getenv(baseEnv)
	}
	if token == "" {
		token = os.Getenv(tokenEnv)
	}
	return tsec.New(tsec.Config{BaseURL: baseURL, Token: token, CheckVPN: checkVPN})
}

func clientFromTSec(cfg *TSecFileConfig, tokenOverride string) (*tsec.Client, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = os.Getenv(cfg.BaseURLEnv)
	}
	token := tokenOverride
	if token == "" {
		token = cfg.Token
	}
	if token == "" {
		token = os.Getenv(cfg.TokenEnv)
	}
	return tsec.New(tsec.Config{BaseURL: baseURL, Token: token, CheckVPN: true})
}

func runsCommand(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("runs requires show, events or cancel")
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
	case "cancel":
		fs := flag.NewFlagSet("runs cancel", flag.ContinueOnError)
		fs.SetOutput(stderr)
		serverURL := fs.String("server", "http://127.0.0.1:8080", "server URL")
		id := fs.String("id", "", "run id")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		req, err := http.NewRequest(http.MethodPost, strings.TrimRight(*serverURL, "/")+"/v1/runs/"+*id+"/cancel", nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("server returned %s", resp.Status)
		}
		_, err = io.Copy(stdout, resp.Body)
		return err
	default:
		return fmt.Errorf("unknown runs command %q", args[0])
	}
}

func imageCommand(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "build" {
		return errors.New("image requires build")
	}
	fs := flag.NewFlagSet("image build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	tag := fs.String("tag", pi.DefaultImage, "image tag")
	contextDir := fs.String("context", "images/pi-kali", "build context")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	cmd := exec.Command("docker", "build", "-t", *tag, "-f", filepath.Join(*contextDir, "Dockerfile"), *contextDir)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func doctor(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", ".harness/harness.db", "database")
	if err := fs.Parse(args); err != nil {
		return err
	}
	commands := [][]string{{"docker", "version", "--format", "{{.Server.Version}}"}, {"docker", "compose", "version"}}
	for _, command := range commands {
		out, err := exec.Command(command[0], command[1:]...).CombinedOutput()
		if err != nil {
			fmt.Fprintf(stdout, "FAIL %s: %s\n", strings.Join(command, " "), strings.TrimSpace(string(out)))
		} else {
			fmt.Fprintf(stdout, "OK   %s: %s\n", strings.Join(command, " "), strings.TrimSpace(string(out)))
		}
	}
	store, err := sqlite.Open(*dbPath)
	if err != nil {
		fmt.Fprintf(stdout, "FAIL sqlite: %v\n", err)
	} else {
		_ = store.Close()
		fmt.Fprintf(stdout, "OK   sqlite: %s\n", *dbPath)
	}
	return nil
}

func migrateCommand(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	task := fs.String("task", "", "legacy task.yaml")
	agent := fs.String("agent", "", "legacy agent.yaml")
	out := fs.String("out", "", "output config file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *task == "" || *agent == "" || *out == "" {
		return errors.New("--task, --agent and --out are required")
	}
	if err := Migrate(*task, *agent, *out); err != nil {
		return err
	}
	fmt.Fprintln(stdout, *out)
	return nil
}

func printResult(w io.Writer, result harness.Result, asJSON bool) {
	if asJSON {
		_ = printJSON(w, result)
		return
	}
	fmt.Fprintf(w, "run=%s status=%s score=%.0f\n", result.RunID, result.Status, scoreOf(result))
	if result.Verdict != nil && result.Verdict.Message != "" {
		fmt.Fprintln(w, "verdict:", result.Verdict.Message)
	}
	if result.Error != "" {
		fmt.Fprintln(w, "error:", result.Error)
	}
}

func scoreOf(result harness.Result) float64 {
	if result.Verdict != nil {
		return result.Verdict.Score
	}
	return 0
}

func printJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func printJSONLine(w io.Writer, value any) error {
	return json.NewEncoder(w).Encode(value)
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func tsecObjective(challenge tsec.Challenge) string {
	description := challenge.Description
	if description == "" {
		description = "No platform description was provided."
	}
	return fmt.Sprintf("Authorized TSec challenge %s.\n\n%s\n\nFind and submit every remaining flag for this challenge.", challenge.UniqueCode, description)
}
