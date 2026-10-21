package cli

import (
	"strings"
	"testing"

	"github.com/red-copilot/harness/tsec"
)

func TestTSecTaskMetadataCarriesChallengeBrief(t *testing.T) {
	metadata := tsecTaskMetadata(tsec.Challenge{
		UniqueCode: "c-07", Description: "Telnet 服务", Difficulty: "easy",
		Level: 1, FlagCount: 1, CorrectFlagCount: 0, ContainerStatus: "running",
	})
	for key, want := range map[string]string{
		"challenge_code":        "c-07",
		"challenge_description": "Telnet 服务",
		"challenge_difficulty":  "easy",
		"flag_count":            "1",
		"correct_flag_count":    "0",
	} {
		if metadata[key] != want {
			t.Fatalf("metadata[%q] = %q, want %q", key, metadata[key], want)
		}
	}
}

func TestTSecObjectiveFallsBackWhenDescriptionMissing(t *testing.T) {
	objective := tsecObjective(tsec.Challenge{UniqueCode: "c-99"})
	if !strings.Contains(objective, "c-99") || !strings.Contains(objective, "No platform description") {
		t.Fatalf("objective = %q", objective)
	}
}
