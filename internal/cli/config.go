package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/sandbox/docker"
	"github.com/red-copilot/harness/verify"
)

type BudgetFile struct {
	WallTimeSeconds int     `json:"wall_time_seconds,omitempty" yaml:"wall_time_seconds,omitempty"`
	MaxTokens       int     `json:"max_tokens,omitempty" yaml:"max_tokens,omitempty"`
	MaxModelCalls   int     `json:"max_model_calls,omitempty" yaml:"max_model_calls,omitempty"`
	MaxToolCalls    int     `json:"max_tool_calls,omitempty" yaml:"max_tool_calls,omitempty"`
	MaxCostUSD      float64 `json:"max_cost_usd,omitempty" yaml:"max_cost_usd,omitempty"`
}

type TSecFileConfig struct {
	Challenge  string `json:"challenge,omitempty" yaml:"challenge,omitempty"`
	All        bool   `json:"all,omitempty" yaml:"all,omitempty"`
	Parallel   int    `json:"parallel,omitempty" yaml:"parallel,omitempty"`
	UseHint    bool   `json:"use_hint,omitempty" yaml:"use_hint,omitempty"`
	BaseURLEnv string `json:"base_url_env,omitempty" yaml:"base_url_env,omitempty"`
	TokenEnv   string `json:"token_env,omitempty" yaml:"token_env,omitempty"`
	BaseURL    string `json:"base_url,omitempty" yaml:"base_url,omitempty"`
	Token      string `json:"token,omitempty" yaml:"token,omitempty"`
}

type TargetFileConfig struct {
	Kind        string   `json:"kind,omitempty" yaml:"kind,omitempty"`
	ID          string   `json:"id,omitempty" yaml:"id,omitempty"`
	Addresses   []string `json:"addresses,omitempty" yaml:"addresses,omitempty"`
	Image       string   `json:"image,omitempty" yaml:"image,omitempty"`
	Command     []string `json:"command,omitempty" yaml:"command,omitempty"`
	Ports       []string `json:"ports,omitempty" yaml:"ports,omitempty"`
	Network     string   `json:"network,omitempty" yaml:"network,omitempty"`
	ComposeFile string   `json:"compose_file,omitempty" yaml:"compose_file,omitempty"`
	ProjectName string   `json:"project_name,omitempty" yaml:"project_name,omitempty"`
	Service     string   `json:"service,omitempty" yaml:"service,omitempty"`
	Port        string   `json:"port,omitempty" yaml:"port,omitempty"`
}

type FileConfig struct {
	APIVersion    string                   `json:"apiVersion" yaml:"apiVersion"`
	Task          harness.Task             `json:"task" yaml:"task"`
	Target        *TargetFileConfig        `json:"target,omitempty" yaml:"target,omitempty"`
	Agents        []harness.AgentSpec      `json:"agents" yaml:"agents"`
	Exploration   *harness.ExplorationSpec `json:"exploration,omitempty" yaml:"exploration,omitempty"`
	Budget        BudgetFile               `json:"budget,omitempty" yaml:"budget,omitempty"`
	SecretEnv     []string                 `json:"secret_env,omitempty" yaml:"secret_env,omitempty"`
	WorkspaceRoot string                   `json:"workspace_root,omitempty" yaml:"workspace_root,omitempty"`
	Verifier      *verify.Command          `json:"verifier,omitempty" yaml:"verifier,omitempty"`
	TSec          *TSecFileConfig          `json:"tsec,omitempty" yaml:"tsec,omitempty"`
}

func LoadConfig(path string) (*FileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg FileConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if cfg.APIVersion == "" {
		cfg.APIVersion = "harness/v1alpha1"
	}
	if cfg.APIVersion != "harness/v1alpha1" {
		return nil, fmt.Errorf("unsupported apiVersion %q", cfg.APIVersion)
	}
	if cfg.TSec != nil && cfg.TSec.Parallel == 0 {
		cfg.TSec.Parallel = 3
	}
	if cfg.TSec != nil && cfg.TSec.BaseURLEnv == "" {
		cfg.TSec.BaseURLEnv = "BENCHMARK_BASE_URL"
	}
	if cfg.TSec != nil && cfg.TSec.TokenEnv == "" {
		cfg.TSec.TokenEnv = "BENCHMARK_TOKEN"
	}
	return &cfg, nil
}

func (c *FileConfig) ToRunSpec() (harness.RunSpec, error) {
	if c.Task.ID == "" || c.Task.Objective == "" {
		return harness.RunSpec{}, errors.New("task.id and task.objective are required")
	}
	if len(c.Agents) == 0 {
		return harness.RunSpec{}, errors.New("at least one agent is required")
	}
	spec := harness.RunSpec{
		Task: c.Task, Agents: c.Agents, Exploration: c.Exploration,
		Budget: harness.Budget{
			WallTime:  time.Duration(c.Budget.WallTimeSeconds) * time.Second,
			MaxTokens: c.Budget.MaxTokens, MaxModelCalls: c.Budget.MaxModelCalls,
			MaxToolCalls: c.Budget.MaxToolCalls, MaxCostUSD: c.Budget.MaxCostUSD,
		},
		SecretEnv: c.SecretEnv, WorkspaceRoot: c.WorkspaceRoot,
	}
	enableDefaultExploration(&spec)
	if c.Verifier != nil {
		spec.Verifier = *c.Verifier
	}
	if c.Target != nil {
		switch c.Target.Kind {
		case "", "external":
			spec.Target = harness.Target{ID: c.Target.ID, Addresses: c.Target.Addresses}
		case "image":
			spec.Backend = docker.ImageBackend{
				Image: c.Target.Image, Command: c.Target.Command, Ports: c.Target.Ports,
				Network: c.Target.Network, Addresses: c.Target.Addresses,
			}
		case "compose":
			spec.Backend = docker.ComposeBackend{
				File: c.Target.ComposeFile, ProjectName: c.Target.ProjectName,
				Service: c.Target.Service, Port: c.Target.Port, Addresses: c.Target.Addresses,
			}
		default:
			return harness.RunSpec{}, fmt.Errorf("unsupported target kind %q", c.Target.Kind)
		}
	}
	return spec, nil
}

func enableDefaultExploration(spec *harness.RunSpec) {
	if spec == nil || spec.Exploration != nil || len(spec.Agents) != 1 {
		return
	}
	spec.Exploration = &harness.ExplorationSpec{Rounds: 6}
}

func applyPiEnvironmentDefaults(spec *harness.RunSpec) {
	provider, providerSet := os.LookupEnv("PI_PROVIDER")
	model, modelSet := os.LookupEnv("PI_MODEL")
	for index := range spec.Agents {
		piSpec := spec.Agents[index].Pi
		if piSpec == nil {
			continue
		}
		if piSpec.Provider == "" && providerSet {
			piSpec.Provider = provider
		}
		if piSpec.Model == "" && modelSet {
			piSpec.Model = model
		}
	}
}

// Migrate converts the legacy Python task/agent YAML into a Go-first config.
// It is intentionally one-way; legacy formats are not runtime contracts.
func Migrate(taskPath, agentPath, outPath string) error {
	var legacyTask struct {
		ID        string `yaml:"id"`
		Objective struct {
			Description string `yaml:"description"`
		} `yaml:"objective"`
	}
	var legacyAgent struct {
		ID      string            `yaml:"id"`
		Image   string            `yaml:"image"`
		Network string            `yaml:"network"`
		Env     map[string]string `yaml:"env"`
		Pi      *legacyPi         `yaml:"pi"`
	}
	if err := decodeYAMLFile(taskPath, &legacyTask); err != nil {
		return fmt.Errorf("read task: %w", err)
	}
	if err := decodeYAMLFile(agentPath, &legacyAgent); err != nil {
		return fmt.Errorf("read agent: %w", err)
	}
	if legacyTask.ID == "" || legacyAgent.ID == "" {
		return errors.New("legacy task and agent must have ids")
	}
	var piSpec *harness.PiSpec
	var capAdd []string
	if legacyAgent.Pi != nil {
		piSpec = &harness.PiSpec{
			Provider: legacyAgent.Pi.Provider, Model: legacyAgent.Pi.Model,
			Thinking: legacyAgent.Pi.Thinking, Tools: legacyAgent.Pi.Tools,
			EnvPassthrough: legacyAgent.Pi.EnvPassthrough,
		}
		capAdd = legacyAgent.Pi.CapAdd
	}
	network := legacyAgent.Network
	if network == "environment" || network == "" {
		network = "bridge"
	}
	cfg := FileConfig{
		APIVersion: "harness/v1alpha1",
		Task:       harness.Task{ID: legacyTask.ID, Objective: legacyTask.Objective.Description},
		Agents: []harness.AgentSpec{{
			ID: legacyAgent.ID, Image: legacyAgent.Image, Network: network,
			Env: legacyAgent.Env, CapAdd: capAdd, Pi: piSpec,
		}},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(outPath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(outPath, data, 0o600)
}

type legacyPi struct {
	Provider       string   `yaml:"provider"`
	Model          string   `yaml:"model"`
	Thinking       string   `yaml:"thinking"`
	Tools          []string `yaml:"tools"`
	EnvPassthrough []string `yaml:"env_passthrough"`
	CapAdd         []string `yaml:"cap_add"`
}

func decodeYAMLFile(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, out)
}
