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
	"github.com/red-copilot/harness/tsec"
	"github.com/red-copilot/harness/verify"
)

type TSecFileConfig struct {
	Challenge  string `json:"challenge,omitempty" yaml:"challenge,omitempty"`
	All        bool   `json:"all,omitempty" yaml:"all,omitempty"`
	Parallel   int    `json:"parallel,omitempty" yaml:"parallel,omitempty"`
	BaseURL    string `json:"base_url,omitempty" yaml:"base_url,omitempty"`
	Token      string `json:"token,omitempty" yaml:"token,omitempty"`
	BaseURLEnv string `json:"base_url_env,omitempty" yaml:"base_url_env,omitempty"`
	TokenEnv   string `json:"token_env,omitempty" yaml:"token_env,omitempty"`
	UseHint    bool   `json:"use_hint,omitempty" yaml:"use_hint,omitempty"`
	CheckVPN   bool   `json:"check_vpn,omitempty" yaml:"check_vpn,omitempty"`
}

// PolicyFile keeps the established flat exploration keys while mapping them
// into the canonical RunPolicy before validation and execution.
type PolicyFile struct {
	MaxOpenIntents         int           `json:"max_open_intents,omitempty" yaml:"max_open_intents,omitempty"`
	MaxTotalIntents        int           `json:"max_total_intents,omitempty" yaml:"max_total_intents,omitempty"`
	MaxAttemptsPerIntent   int           `json:"max_attempts_per_intent,omitempty" yaml:"max_attempts_per_intent,omitempty"`
	MaxToolCallsPerAttempt int           `json:"max_tool_calls_per_attempt,omitempty" yaml:"max_tool_calls_per_attempt,omitempty"`
	MaxWallTimePerAttempt  time.Duration `json:"max_wall_time_per_attempt,omitempty" yaml:"max_wall_time_per_attempt,omitempty"`
	ConcludeTimeout        time.Duration `json:"conclude_timeout,omitempty" yaml:"conclude_timeout,omitempty"`
	StagnationRounds       int           `json:"stagnation_rounds,omitempty" yaml:"stagnation_rounds,omitempty"`
	MaxReasonerToolCalls   int           `json:"max_reasoner_tool_calls,omitempty" yaml:"max_reasoner_tool_calls,omitempty"`
	MaxReasonerWallTime    time.Duration `json:"max_reasoner_wall_time,omitempty" yaml:"max_reasoner_wall_time,omitempty"`
	MaxReasonerFailures    int           `json:"max_reasoner_failures,omitempty" yaml:"max_reasoner_failures,omitempty"`
	BootstrapEnabled       *bool         `json:"bootstrap_enabled,omitempty" yaml:"bootstrap_enabled,omitempty"`
}

func (p PolicyFile) toRunPolicy() harness.RunPolicy {
	policy := harness.DefaultRunPolicy()
	policy.Exploration = harness.ExplorationPolicy{
		MaxOpenIntents: p.MaxOpenIntents, MaxTotalIntents: p.MaxTotalIntents,
		MaxAttemptsPerIntent: p.MaxAttemptsPerIntent, MaxToolCallsPerAttempt: p.MaxToolCallsPerAttempt,
		MaxWallTimePerAttempt: p.MaxWallTimePerAttempt, ConcludeTimeout: p.ConcludeTimeout,
		StagnationRounds: p.StagnationRounds, MaxReasonerToolCalls: p.MaxReasonerToolCalls,
		MaxReasonerWallTime: p.MaxReasonerWallTime, MaxReasonerFailures: p.MaxReasonerFailures,
		BootstrapEnabled: p.BootstrapEnabled,
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
	TSec          *TSecFileConfig  `json:"tsec,omitempty" yaml:"tsec,omitempty"`
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
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if cfg.APIVersion == "" {
		cfg.APIVersion = "harness/v3"
	}
	if cfg.APIVersion != "harness/v3" {
		return nil, fmt.Errorf("unsupported apiVersion %q", cfg.APIVersion)
	}
	if cfg.Target.Isolation == "" {
		cfg.Target.Isolation = string(harness.IsolationCallerManaged)
	}
	if cfg.TSec != nil {
		if cfg.TSec.BaseURLEnv == "" {
			cfg.TSec.BaseURLEnv = "BENCHMARK_BASE_URL"
		}
		if cfg.TSec.TokenEnv == "" {
			cfg.TSec.TokenEnv = "BENCHMARK_TOKEN"
		}
		if cfg.TSec.Parallel == 0 {
			cfg.TSec.Parallel = 3
		}
	}
	return &cfg, nil
}

// Descriptor projects the serializable v3 Run contract. Adapter instances are
// deliberately absent and are composed only by ToRunSpec at the deployment edge.
func (c *FileConfig) Descriptor() (harness.RunDescriptor, error) {
	reasoner, err := c.Reasoner.descriptor()
	if err != nil {
		return harness.RunDescriptor{}, fmt.Errorf("reasoner: %w", err)
	}
	executor, err := c.Executor.descriptor()
	if err != nil {
		return harness.RunDescriptor{}, fmt.Errorf("executor: %w", err)
	}
	return harness.RunDescriptor{
		SchemaVersion: harness.CurrentSchemaVersion,
		Task:          c.Task,
		Target:        harness.TargetSpec{ID: c.Target.ID, Addresses: append([]string(nil), c.Target.Addresses...), Isolation: harness.IsolationMode(c.Target.Isolation)},
		Reasoner:      reasoner,
		Executor:      executor,
		Policy:        c.Policy.toRunPolicy(),
	}, nil
}

// ToRunSpec composes serializable configuration with deployment adapters.
func (c *FileConfig) ToRunSpec(artifacts harness.ArtifactStore) (harness.RunSpec, error) {
	if artifacts == nil {
		return harness.RunSpec{}, errors.New("artifact store is required")
	}
	target := harness.TargetSpec{ID: c.Target.ID, Addresses: append([]string(nil), c.Target.Addresses...), Isolation: harness.IsolationMode(c.Target.Isolation)}
	policy := c.Policy.toRunPolicy()
	policy.Budget = harness.Budget{WallTime: time.Duration(c.Budget.WallTimeSeconds) * time.Second, MaxTokens: c.Budget.MaxTokens, MaxModelCalls: c.Budget.MaxModelCalls, MaxToolCalls: c.Budget.MaxToolCalls, MaxCostUSD: c.Budget.MaxCostUSD}
	spec := harness.RunSpec{Task: c.Task, Target: target, Policy: policy, Artifacts: artifacts, SecretEnv: append([]string(nil), c.SecretEnv...), WorkspaceRoot: c.WorkspaceRoot, MaterializeSkills: pi.MaterializeSkills}
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
	case "tsec":
		if c.TSec == nil {
			return harness.RunSpec{}, errors.New("tsec configuration is required for target.kind tsec")
		}
		baseURL := c.TSec.BaseURL
		if baseURL == "" {
			baseURL = os.Getenv(c.TSec.BaseURLEnv)
		}
		token := c.TSec.Token
		if token == "" {
			token = os.Getenv(c.TSec.TokenEnv)
		}
		client, err := tsec.New(tsec.Config{BaseURL: baseURL, Token: token, CheckVPN: c.TSec.CheckVPN})
		if err != nil {
			return harness.RunSpec{}, err
		}
		challengeCode := c.Target.ID
		if c.TSec.Challenge != "" {
			challengeCode = c.TSec.Challenge
		}
		backend := tsec.NewBackend(client, challengeCode)
		backend.CheckVPN = c.TSec.CheckVPN
		backend.UseHint = c.TSec.UseHint
		// TSec both provisions the challenge instance and is its authority.
		spec.Provisioner = backend
		spec.Backend = backend
		spec.KnownSecrets = appendUniqueString(spec.KnownSecrets, token)
		spec.SecretEnv = appendUniqueString(spec.SecretEnv, c.TSec.TokenEnv)
		spec.Executor.EnvFrom = appendUniqueString(spec.Executor.EnvFrom, c.TSec.BaseURLEnv, c.TSec.TokenEnv)
	default:
		return harness.RunSpec{}, fmt.Errorf("unsupported target kind %q", c.Target.Kind)
	}
	if c.Verifier != nil {
		spec.Verifier = *c.Verifier
	}
	return spec, nil
}

func (a AgentFileSpec) descriptor() (harness.AgentDescriptor, error) {
	spec, err := a.toHarness()
	if err != nil {
		return harness.AgentDescriptor{}, err
	}
	return spec.Descriptor(), nil
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

func appendUniqueString(values []string, additions ...string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values)+len(additions))
	for _, value := range append(append([]string(nil), values...), additions...) {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
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
