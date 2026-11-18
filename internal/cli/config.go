package cli

import (
	"bytes"
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

// PolicyFile keeps the established flat exploration keys while mapping them
// into the canonical RunPolicy before validation and execution.
type PolicyFile struct {
	MaxTotalIntents             int           `json:"max_total_intents,omitempty" yaml:"max_total_intents,omitempty"`
	MaxAttemptsPerIntent        int           `json:"max_attempts_per_intent,omitempty" yaml:"max_attempts_per_intent,omitempty"`
	MaxToolCallsPerAttempt      int           `json:"max_tool_calls_per_attempt,omitempty" yaml:"max_tool_calls_per_attempt,omitempty"`
	MaxWallTimePerAttempt       time.Duration `json:"max_wall_time_per_attempt,omitempty" yaml:"max_wall_time_per_attempt,omitempty"`
	ConcludeTimeout             time.Duration `json:"conclude_timeout,omitempty" yaml:"conclude_timeout,omitempty"`
	StagnationRounds            int           `json:"stagnation_rounds,omitempty" yaml:"stagnation_rounds,omitempty"`
	MaxReasonerToolCalls        int           `json:"max_reasoner_tool_calls,omitempty" yaml:"max_reasoner_tool_calls,omitempty"`
	MaxReasonerWallTime         time.Duration `json:"max_reasoner_wall_time,omitempty" yaml:"max_reasoner_wall_time,omitempty"`
	MaxReasonerFailures         int           `json:"max_reasoner_failures,omitempty" yaml:"max_reasoner_failures,omitempty"`
	MaxAuthorityObserveFailures int           `json:"max_authority_observe_failures,omitempty" yaml:"max_authority_observe_failures,omitempty"`
	EnginePollInterval          time.Duration `json:"engine_poll_interval,omitempty" yaml:"engine_poll_interval,omitempty"`
	DrainTimeout                time.Duration `json:"drain_timeout,omitempty" yaml:"drain_timeout,omitempty"`
	AbortTimeout                time.Duration `json:"abort_timeout,omitempty" yaml:"abort_timeout,omitempty"`
}

func (p PolicyFile) toRunPolicy() harness.RunPolicy {
	policy := harness.DefaultRunPolicy()
	policy.Exploration = harness.ExplorationPolicy{
		MaxTotalIntents:      p.MaxTotalIntents,
		MaxAttemptsPerIntent: p.MaxAttemptsPerIntent, MaxToolCallsPerAttempt: p.MaxToolCallsPerAttempt,
		MaxWallTimePerAttempt: p.MaxWallTimePerAttempt, ConcludeTimeout: p.ConcludeTimeout,
		StagnationRounds: p.StagnationRounds, MaxReasonerToolCalls: p.MaxReasonerToolCalls,
		MaxReasonerWallTime: p.MaxReasonerWallTime, MaxReasonerFailures: p.MaxReasonerFailures,
		MaxAuthorityObserveFailures: p.MaxAuthorityObserveFailures,
		EnginePollInterval:          p.EnginePollInterval, DrainTimeout: p.DrainTimeout, AbortTimeout: p.AbortTimeout,
	}
	return harness.NormalizeRunPolicy(policy)
}

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
	Network       string               `json:"network,omitempty" yaml:"network,omitempty"`
	CapAdd        []string             `json:"cap_add,omitempty" yaml:"cap_add,omitempty"`
	Resources     harness.ResourceSpec `json:"resources,omitempty" yaml:"resources,omitempty"`
	RuntimeConfig any                  `json:"runtime,omitempty" yaml:"runtime,omitempty"`
}

type FileConfig struct {
	APIVersion    string           `json:"apiVersion" yaml:"apiVersion"`
	Task          harness.Task     `json:"task" yaml:"task"`
	Target        TargetFileConfig `json:"target" yaml:"target"`
	Reasoner      AgentFileSpec    `json:"reasoner" yaml:"reasoner"`
	Executor      AgentFileSpec    `json:"executor" yaml:"executor"`
	Policy        PolicyFile       `json:"policy,omitempty" yaml:"policy,omitempty"`
	Budget        BudgetFile       `json:"budget,omitempty" yaml:"budget,omitempty"`
	SecretEnv     []string         `json:"secret_env,omitempty" yaml:"secret_env,omitempty"`
	WorkspaceRoot string           `json:"workspace_root,omitempty" yaml:"workspace_root,omitempty"`
	Verifier      *verify.Command  `json:"verifier,omitempty" yaml:"verifier,omitempty"`
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
			return nil, fmt.Errorf("legacy key %q is unsupported; use apiVersion harness/v4 (see examples/run.yaml)", legacy)
		}
	}
	var cfg FileConfig
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if cfg.APIVersion == "" {
		cfg.APIVersion = "harness/v4"
	}
	if cfg.APIVersion != "harness/v4" {
		return nil, fmt.Errorf("unsupported apiVersion %q", cfg.APIVersion)
	}
	if cfg.Target.Isolation == "" {
		cfg.Target.Isolation = string(harness.IsolationCallerManaged)
	}
	return &cfg, nil
}

// ToRunSpec composes serializable configuration with deployment adapters.
func (c *FileConfig) ToRunSpec(artifacts harness.ArtifactStore) (harness.RunSpec, error) {
	if artifacts == nil {
		return harness.RunSpec{}, errors.New("artifact store is required")
	}
	target := harness.TargetSpec{ID: c.Target.ID, Addresses: append([]string(nil), c.Target.Addresses...), Isolation: harness.IsolationMode(c.Target.Isolation)}
	policy := c.Policy.toRunPolicy()
	policy.Budget = harness.Budget{WallTime: time.Duration(c.Budget.WallTimeSeconds) * time.Second, MaxTokens: c.Budget.MaxTokens, MaxModelCalls: c.Budget.MaxModelCalls, MaxToolCalls: c.Budget.MaxToolCalls, MaxCostUSD: c.Budget.MaxCostUSD}
	spec := harness.RunSpec{Task: c.Task, Target: target, Policy: policy, Artifacts: artifacts, SecretEnv: append([]string(nil), c.SecretEnv...), WorkspaceRoot: c.WorkspaceRoot}
	var err error
	reasoner, convertErr := c.Reasoner.toHarness()
	if convertErr != nil {
		return harness.RunSpec{}, fmt.Errorf("reasoner: %w", convertErr)
	}
	spec.Reasoner, err = composeAgent(reasoner)
	if err != nil {
		return harness.RunSpec{}, fmt.Errorf("reasoner: %w", err)
	}
	executor, convertErr := c.Executor.toHarness()
	if convertErr != nil {
		return harness.RunSpec{}, fmt.Errorf("executor: %w", convertErr)
	}
	executor, err = composeAgent(executor)
	if err != nil {
		return harness.RunSpec{}, fmt.Errorf("executor: %w", err)
	}
	spec.Executor = executor
	switch c.Target.Kind {
	case "", "external":
	case "image":
		spec.Provisioner = docker.ImageBackend{Image: c.Target.Image, Command: c.Target.Command, Ports: c.Target.Ports, Network: c.Target.Network, Addresses: c.Target.Addresses}
	case "compose":
		spec.Provisioner = docker.ComposeBackend{File: c.Target.ComposeFile, ProjectName: c.Target.ProjectName, Service: c.Target.Service, Port: c.Target.Port, Addresses: c.Target.Addresses}
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
	return harness.AgentSpec{ID: a.ID, Image: a.Image, Command: a.Command, Env: a.Env, EnvFrom: a.EnvFrom, Mounts: a.Mounts, Network: a.Network, CapAdd: a.CapAdd, Resources: a.Resources, RuntimeConfig: runtimeConfig}, nil
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
