package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/pi"
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

type TargetFileConfig struct {
	Kind        string   `json:"kind,omitempty" yaml:"kind,omitempty"`
	ID          string   `json:"id,omitempty" yaml:"id,omitempty"`
	Addresses   []string `json:"addresses,omitempty" yaml:"addresses,omitempty"`
	Isolation   string   `json:"isolation,omitempty" yaml:"isolation,omitempty"`
	Image       string   `json:"image,omitempty" yaml:"image,omitempty"`
	Command     []string `json:"command,omitempty" yaml:"command,omitempty"`
	Ports       []string `json:"ports,omitempty" yaml:"ports,omitempty"`
	Network     string   `json:"network,omitempty" yaml:"network,omitempty"`
	ComposeFile string   `json:"compose_file,omitempty" yaml:"compose_file,omitempty"`
	ProjectName string   `json:"project_name,omitempty" yaml:"project_name,omitempty"`
	Service     string   `json:"service,omitempty" yaml:"service,omitempty"`
	Port        string   `json:"port,omitempty" yaml:"port,omitempty"`
}

type AgentFileSpec struct {
	ID            string               `json:"id" yaml:"id"`
	Image         string               `json:"image,omitempty" yaml:"image,omitempty"`
	Command       []string             `json:"command,omitempty" yaml:"command,omitempty"`
	Env           map[string]string    `json:"env,omitempty" yaml:"env,omitempty"`
	EnvFrom       []string             `json:"env_from,omitempty" yaml:"env_from,omitempty"`
	Mounts        []harness.Mount      `json:"mounts,omitempty" yaml:"mounts,omitempty"`
	Skills        []harness.Mount      `json:"skills,omitempty" yaml:"skills,omitempty"`
	Network       string               `json:"network,omitempty" yaml:"network,omitempty"`
	CapAdd        []string             `json:"cap_add,omitempty" yaml:"cap_add,omitempty"`
	Resources     harness.ResourceSpec `json:"resources,omitempty" yaml:"resources,omitempty"`
	RuntimeConfig any                  `json:"runtime,omitempty" yaml:"runtime,omitempty"`
}

type FileConfig struct {
	APIVersion    string                    `json:"apiVersion" yaml:"apiVersion"`
	Task          harness.Task              `json:"task" yaml:"task"`
	Target        TargetFileConfig          `json:"target" yaml:"target"`
	Planner       AgentFileSpec             `json:"planner" yaml:"planner"`
	Executors     []AgentFileSpec           `json:"executors" yaml:"executors"`
	Policy        harness.ExplorationPolicy `json:"policy,omitempty" yaml:"policy,omitempty"`
	Budget        BudgetFile                `json:"budget,omitempty" yaml:"budget,omitempty"`
	SecretEnv     []string                  `json:"secret_env,omitempty" yaml:"secret_env,omitempty"`
	WorkspaceRoot string                    `json:"workspace_root,omitempty" yaml:"workspace_root,omitempty"`
	Verifier      *verify.Command           `json:"verifier,omitempty" yaml:"verifier,omitempty"`
}

func LoadConfig(path string) (*FileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	for _, legacy := range []string{"workflow", "pi", "agents", "targets", "exploration"} {
		if _, ok := raw[legacy]; ok {
			return nil, fmt.Errorf("legacy key %q is unsupported; see docs/migration-v1.md", legacy)
		}
	}
	var cfg FileConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if cfg.APIVersion == "" {
		cfg.APIVersion = "harness/v1"
	}
	if cfg.APIVersion != "harness/v1" {
		return nil, fmt.Errorf("unsupported apiVersion %q", cfg.APIVersion)
	}
	if cfg.Target.Isolation == "" {
		cfg.Target.Isolation = string(harness.IsolationCallerManaged)
	}
	if cfg.Policy.MaxDepth == 0 {
		cfg.Policy = harness.DefaultExplorationPolicy()
	}
	return &cfg, nil
}

// ToRunSpec composes serializable configuration with deployment adapters.
func (c *FileConfig) ToRunSpec(artifacts harness.ArtifactStore) (harness.RunSpec, error) {
	if artifacts == nil {
		return harness.RunSpec{}, errors.New("artifact store is required")
	}
	target := harness.Target{ID: c.Target.ID, Addresses: append([]string(nil), c.Target.Addresses...), Isolation: harness.IsolationMode(c.Target.Isolation)}
	spec := harness.RunSpec{Task: c.Task, Target: target, Policy: harness.NormalizeExplorationPolicy(c.Policy), Budget: harness.Budget{WallTime: time.Duration(c.Budget.WallTimeSeconds) * time.Second, MaxTokens: c.Budget.MaxTokens, MaxModelCalls: c.Budget.MaxModelCalls, MaxToolCalls: c.Budget.MaxToolCalls, MaxCostUSD: c.Budget.MaxCostUSD}, Artifacts: artifacts, SecretEnv: append([]string(nil), c.SecretEnv...), WorkspaceRoot: c.WorkspaceRoot}
	var err error
	planner, convertErr := c.Planner.toHarness()
	if convertErr != nil {
		return harness.RunSpec{}, fmt.Errorf("planner: %w", convertErr)
	}
	spec.Planner, err = composeAgent(planner)
	if err != nil {
		return harness.RunSpec{}, fmt.Errorf("planner: %w", err)
	}
	for i := range c.Executors {
		executor, convertErr := c.Executors[i].toHarness()
		if convertErr != nil {
			return harness.RunSpec{}, fmt.Errorf("executor %d: %w", i, convertErr)
		}
		executor, err = composeAgent(executor)
		if err != nil {
			return harness.RunSpec{}, fmt.Errorf("executor %d: %w", i, err)
		}
		spec.Executors = append(spec.Executors, executor)
	}
	switch c.Target.Kind {
	case "", "external":
	case "image":
		spec.Backend = docker.ImageBackend{Image: c.Target.Image, Command: c.Target.Command, Ports: c.Target.Ports, Network: c.Target.Network, Addresses: c.Target.Addresses}
	case "compose":
		spec.Backend = docker.ComposeBackend{File: c.Target.ComposeFile, ProjectName: c.Target.ProjectName, Service: c.Target.Service, Port: c.Target.Port, Addresses: c.Target.Addresses}
	default:
		return harness.RunSpec{}, fmt.Errorf("unsupported target kind %q", c.Target.Kind)
	}
	if c.Verifier != nil {
		spec.Verifier = *c.Verifier
	}
	return spec, nil
}

func (a AgentFileSpec) toHarness() (harness.AgentSpec, error) {
	var runtimeConfig json.RawMessage
	if a.RuntimeConfig != nil {
		encoded, err := json.Marshal(a.RuntimeConfig)
		if err != nil {
			return harness.AgentSpec{}, err
		}
		runtimeConfig = encoded
	}
	return harness.AgentSpec{ID: a.ID, Image: a.Image, Command: a.Command, Env: a.Env, EnvFrom: a.EnvFrom, Mounts: a.Mounts, Skills: a.Skills, Network: a.Network, CapAdd: a.CapAdd, Resources: a.Resources, RuntimeConfig: runtimeConfig}, nil
}

func composeAgent(agent harness.AgentSpec) (harness.AgentSpec, error) {
	config := pi.Config{}
	if len(agent.RuntimeConfig) > 0 {
		if err := json.Unmarshal(agent.RuntimeConfig, &config); err != nil {
			return agent, fmt.Errorf("invalid pi runtime config: %w", err)
		}
	}
	agent.Runtime = pi.NewFactory(config)
	return agent, nil
}

func ApplyPiEnvironmentDefaults(descriptor *harness.RunDescriptor) {
	provider, providerSet := os.LookupEnv("PI_PROVIDER")
	model, modelSet := os.LookupEnv("PI_MODEL")
	if !providerSet && !modelSet {
		return
	}
	apply := func(agent *harness.AgentSpec) {
		var config pi.Config
		if len(agent.RuntimeConfig) > 0 {
			_ = json.Unmarshal(agent.RuntimeConfig, &config)
		}
		if config.Provider == "" && providerSet {
			config.Provider = provider
		}
		if config.Model == "" && modelSet {
			config.Model = model
		}
		agent.RuntimeConfig, _ = json.Marshal(config)
	}
	apply(&descriptor.Planner)
	for i := range descriptor.Executors {
		apply(&descriptor.Executors[i])
	}
}
