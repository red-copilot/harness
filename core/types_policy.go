package core

import (
	"fmt"
	"time"
)

// ExplorationPolicy contains all mechanically enforced Claim/Intent limits.
type ExplorationPolicy struct {
	MaxOpenIntents         int           `json:"max_open_intents" yaml:"max_open_intents"`
	MaxTotalIntents        int           `json:"max_total_intents" yaml:"max_total_intents"`
	MaxAttemptsPerIntent   int           `json:"max_attempts_per_intent" yaml:"max_attempts_per_intent"`
	MaxToolCallsPerAttempt int           `json:"max_tool_calls_per_attempt" yaml:"max_tool_calls_per_attempt"`
	MaxWallTimePerAttempt  time.Duration `json:"max_wall_time_per_attempt" yaml:"max_wall_time_per_attempt"`
	ConcludeTimeout        time.Duration `json:"conclude_timeout" yaml:"conclude_timeout"`
	StagnationRounds       int           `json:"stagnation_rounds" yaml:"stagnation_rounds"`
	MaxReasonerToolCalls   int           `json:"max_reasoner_tool_calls" yaml:"max_reasoner_tool_calls"`
	MaxReasonerWallTime    time.Duration `json:"max_reasoner_wall_time" yaml:"max_reasoner_wall_time"`
	MaxReasonerFailures    int           `json:"max_reasoner_failures" yaml:"max_reasoner_failures"`
	BootstrapEnabled       *bool         `json:"bootstrap_enabled,omitempty" yaml:"bootstrap_enabled,omitempty"`
}

// ContextPolicy owns deterministic model-context bounds.
type ContextPolicy struct {
	MaxBoardItems     int   `json:"max_board_items" yaml:"max_board_items"`
	MaxBoardBytes     int64 `json:"max_board_bytes" yaml:"max_board_bytes"`
	MaxClaimBytes     int   `json:"max_claim_bytes" yaml:"max_claim_bytes"`
	MaxIntentBytes    int   `json:"max_intent_bytes" yaml:"max_intent_bytes"`
	MaxHintBytes      int   `json:"max_hint_bytes" yaml:"max_hint_bytes"`
	MaxAssistantBytes int   `json:"max_assistant_bytes" yaml:"max_assistant_bytes"`
	MaxReportBytes    int64 `json:"max_report_bytes" yaml:"max_report_bytes"`
}

// EvidencePolicy owns report and artifact bounds.
type EvidencePolicy struct {
	MaxReportBytes             int64 `json:"max_report_bytes" yaml:"max_report_bytes"`
	MaxEvidenceFilesPerAttempt int   `json:"max_evidence_files_per_attempt" yaml:"max_evidence_files_per_attempt"`
	MaxArtifactBytes           int64 `json:"max_artifact_bytes" yaml:"max_artifact_bytes"`
	MaxAttemptArtifactBytes    int64 `json:"max_attempt_artifact_bytes" yaml:"max_attempt_artifact_bytes"`
}

// RunPolicy groups bounds by the component that mechanically enforces them.
type RunPolicy struct {
	Budget      Budget            `json:"budget" yaml:"budget"`
	Exploration ExplorationPolicy `json:"exploration" yaml:"exploration"`
	Context     ContextPolicy     `json:"context" yaml:"context"`
	Evidence    EvidencePolicy    `json:"evidence" yaml:"evidence"`
}

// AttemptBudget bounds one Intent attempt.
type AttemptBudget struct {
	MaxToolCalls int           `json:"max_tool_calls"`
	MaxWallTime  time.Duration `json:"max_wall_time"`
}

// DefaultRunPolicy returns the v2 policy defaults.
func DefaultRunPolicy() RunPolicy {
	bootstrap := true
	return RunPolicy{
		Exploration: ExplorationPolicy{
			MaxOpenIntents: 2, MaxTotalIntents: 20, MaxAttemptsPerIntent: 3,
			MaxToolCallsPerAttempt: 20, MaxWallTimePerAttempt: 10 * time.Minute,
			ConcludeTimeout: 90 * time.Second, StagnationRounds: 3,
			MaxReasonerToolCalls: 4, MaxReasonerWallTime: 2 * time.Minute,
			MaxReasonerFailures: 3, BootstrapEnabled: &bootstrap,
		},
		Context: ContextPolicy{
			MaxBoardItems: 64, MaxBoardBytes: 64 << 10, MaxClaimBytes: 2 << 10,
			MaxIntentBytes: 2 << 10, MaxHintBytes: 8 << 10,
			MaxAssistantBytes: 1 << 20, MaxReportBytes: 1 << 20,
		},
		Evidence: EvidencePolicy{
			MaxReportBytes: 1 << 20, MaxEvidenceFilesPerAttempt: 16,
			MaxArtifactBytes: 10 << 20, MaxAttemptArtifactBytes: 50 << 20,
		},
	}
}

// NormalizeRunPolicy fills only zero values. Negative values remain visible to
// validation so they cannot silently become unlimited access.
func NormalizeRunPolicy(policy RunPolicy) RunPolicy {
	defaults := DefaultRunPolicy()
	e, d := &policy.Exploration, defaults.Exploration
	if e.MaxOpenIntents == 0 {
		e.MaxOpenIntents = d.MaxOpenIntents
	}
	if e.MaxTotalIntents == 0 {
		e.MaxTotalIntents = d.MaxTotalIntents
	}
	if e.MaxAttemptsPerIntent == 0 {
		e.MaxAttemptsPerIntent = d.MaxAttemptsPerIntent
	}
	if e.MaxToolCallsPerAttempt == 0 {
		e.MaxToolCallsPerAttempt = d.MaxToolCallsPerAttempt
	}
	if e.MaxWallTimePerAttempt == 0 {
		e.MaxWallTimePerAttempt = d.MaxWallTimePerAttempt
	}
	if e.ConcludeTimeout == 0 {
		e.ConcludeTimeout = d.ConcludeTimeout
	}
	if e.StagnationRounds == 0 {
		e.StagnationRounds = d.StagnationRounds
	}
	if e.MaxReasonerToolCalls == 0 {
		e.MaxReasonerToolCalls = d.MaxReasonerToolCalls
	}
	if e.MaxReasonerWallTime == 0 {
		e.MaxReasonerWallTime = d.MaxReasonerWallTime
	}
	if e.MaxReasonerFailures == 0 {
		e.MaxReasonerFailures = d.MaxReasonerFailures
	}
	if e.BootstrapEnabled == nil {
		e.BootstrapEnabled = d.BootstrapEnabled
	}
	c, dc := &policy.Context, defaults.Context
	if c.MaxBoardItems == 0 {
		c.MaxBoardItems = dc.MaxBoardItems
	}
	if c.MaxBoardBytes == 0 {
		c.MaxBoardBytes = dc.MaxBoardBytes
	}
	if c.MaxClaimBytes == 0 {
		c.MaxClaimBytes = dc.MaxClaimBytes
	}
	if c.MaxIntentBytes == 0 {
		c.MaxIntentBytes = dc.MaxIntentBytes
	}
	if c.MaxHintBytes == 0 {
		c.MaxHintBytes = dc.MaxHintBytes
	}
	if c.MaxAssistantBytes == 0 {
		c.MaxAssistantBytes = dc.MaxAssistantBytes
	}
	if c.MaxReportBytes == 0 {
		c.MaxReportBytes = dc.MaxReportBytes
	}
	ev, de := &policy.Evidence, defaults.Evidence
	if ev.MaxReportBytes == 0 {
		ev.MaxReportBytes = de.MaxReportBytes
	}
	if ev.MaxEvidenceFilesPerAttempt == 0 {
		ev.MaxEvidenceFilesPerAttempt = de.MaxEvidenceFilesPerAttempt
	}
	if ev.MaxArtifactBytes == 0 {
		ev.MaxArtifactBytes = de.MaxArtifactBytes
	}
	if ev.MaxAttemptArtifactBytes == 0 {
		ev.MaxAttemptArtifactBytes = de.MaxAttemptArtifactBytes
	}
	return policy
}

// ValidateRunPolicy rejects negative limits before a Run record is created.
func ValidateRunPolicy(policy RunPolicy) error {
	limits := []struct {
		name  string
		value int64
	}{
		{"max_open_intents", int64(policy.Exploration.MaxOpenIntents)},
		{"max_total_intents", int64(policy.Exploration.MaxTotalIntents)},
		{"max_attempts_per_intent", int64(policy.Exploration.MaxAttemptsPerIntent)},
		{"max_tool_calls_per_attempt", int64(policy.Exploration.MaxToolCallsPerAttempt)},
		{"stagnation_rounds", int64(policy.Exploration.StagnationRounds)},
		{"max_reasoner_tool_calls", int64(policy.Exploration.MaxReasonerToolCalls)},
		{"max_reasoner_failures", int64(policy.Exploration.MaxReasonerFailures)},
		{"max_board_items", int64(policy.Context.MaxBoardItems)},
		{"max_board_bytes", policy.Context.MaxBoardBytes},
		{"max_claim_bytes", int64(policy.Context.MaxClaimBytes)},
		{"max_intent_bytes", int64(policy.Context.MaxIntentBytes)},
		{"max_hint_bytes", int64(policy.Context.MaxHintBytes)},
		{"max_assistant_bytes", int64(policy.Context.MaxAssistantBytes)},
		{"max_report_bytes", policy.Context.MaxReportBytes},
		{"max_evidence_files_per_attempt", int64(policy.Evidence.MaxEvidenceFilesPerAttempt)},
		{"max_artifact_bytes", policy.Evidence.MaxArtifactBytes},
		{"max_attempt_artifact_bytes", policy.Evidence.MaxAttemptArtifactBytes},
		{"max_tokens", int64(policy.Budget.MaxTokens)},
		{"max_model_calls", int64(policy.Budget.MaxModelCalls)},
		{"max_tool_calls", int64(policy.Budget.MaxToolCalls)},
	}
	for _, limit := range limits {
		if limit.value < 0 {
			return fmt.Errorf("%s must not be negative", limit.name)
		}
	}
	if policy.Exploration.MaxOpenIntents < 1 || policy.Exploration.MaxOpenIntents > 2 {
		return fmt.Errorf("max_open_intents must be 1 or 2")
	}
	if policy.Exploration.MaxWallTimePerAttempt < 0 || policy.Exploration.MaxReasonerWallTime < 0 || policy.Exploration.ConcludeTimeout < 0 || policy.Budget.WallTime < 0 {
		return fmt.Errorf("durations must not be negative")
	}
	if policy.Budget.MaxCostUSD < 0 {
		return fmt.Errorf("max_cost_usd must not be negative")
	}
	return nil
}
