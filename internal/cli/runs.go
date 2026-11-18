package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/store/sqlite"
)

// runSummary is the operator-facing view of one Run: identity, terminal
// state, and bounded counts instead of the raw embedded Spec/Result blobs.
type runSummary struct {
	RunID      string `json:"run_id"`
	Phase      string `json:"phase"`
	Outcome    string `json:"outcome,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
	TargetID   string `json:"target_id,omitempty"`
	Error      string `json:"error,omitempty"`
	CreatedAt  string `json:"created_at"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	Claims     int    `json:"claims,omitempty"`
	Intents    int    `json:"intents,omitempty"`
	Attempts   int    `json:"attempts,omitempty"`
	Artifacts  int    `json:"artifacts,omitempty"`
	// DroppedLiveEvents reports events that a slow consumer missed on the live
	// stream; the canonical store always holds them.
	DroppedLiveEvents uint64 `json:"dropped_live_events,omitempty"`
}

func summarizeRun(record harness.RunRecord) runSummary {
	summary := runSummary{
		RunID: record.ID, Phase: string(record.Phase), Outcome: string(record.Outcome),
		Error: record.Error, CreatedAt: record.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
	if record.StartedAt != nil {
		summary.StartedAt = record.StartedAt.Format("2006-01-02T15:04:05Z")
	}
	if record.FinishedAt != nil {
		summary.FinishedAt = record.FinishedAt.Format("2006-01-02T15:04:05Z")
	}
	// The persisted Spec is a descriptor projection; extract only identity
	// fields, never the whole blob.
	if len(record.Spec) > 0 {
		var descriptor struct {
			Task   struct{ ID string } `json:"task"`
			Target struct{ ID string } `json:"target"`
		}
		if err := json.Unmarshal(record.Spec, &descriptor); err == nil {
			summary.TaskID, summary.TargetID = descriptor.Task.ID, descriptor.Target.ID
		}
	}
	if len(record.Metadata) > 0 {
		var metadata struct {
			DroppedLiveEvents uint64 `json:"dropped_live_events"`
		}
		if err := json.Unmarshal(record.Metadata, &metadata); err == nil {
			summary.DroppedLiveEvents = metadata.DroppedLiveEvents
		}
	}
	return summary
}

// runsList implements `harness runs list`.
func runsList(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("runs list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", ".harness/harness.db", "database")
	limit := fs.Int("limit", 50, "maximum runs to list")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	store, err := sqlite.Open(*dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	records, err := store.ListRuns(context.Background(), *limit)
	if err != nil {
		return err
	}
	if *asJSON {
		summaries := make([]runSummary, 0, len(records))
		for _, record := range records {
			summaries = append(summaries, summarizeRun(record))
		}
		return printJSON(stdout, summaries)
	}
	for _, record := range records {
		summary := summarizeRun(record)
		fmt.Fprintf(stdout, "%s %-11s %-11s %s\n", summary.RunID, summary.Phase, summary.Outcome, summary.CreatedAt)
	}
	return nil
}

// runsShow implements `harness runs show` with a structured summary.
func runsShow(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("runs show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", ".harness/harness.db", "database")
	id := fs.String("id", "", "run id")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
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
	ctx := context.Background()
	record, err := store.GetRun(ctx, *id)
	if err != nil {
		return err
	}
	summary := summarizeRun(record)
	claims, err := store.ListClaims(ctx, *id)
	if err != nil {
		return err
	}
	intents, err := store.ListIntents(ctx, *id)
	if err != nil {
		return err
	}
	attempts, err := store.ListIntentAttempts(ctx, *id)
	if err != nil {
		return err
	}
	artifacts, err := store.ListArtifacts(ctx, *id)
	if err != nil {
		return err
	}
	summary.Claims, summary.Intents, summary.Attempts, summary.Artifacts = len(claims), len(intents), len(attempts), len(artifacts)
	if *asJSON {
		return printJSON(stdout, summary)
	}
	fmt.Fprintf(stdout, "run       %s\n", summary.RunID)
	fmt.Fprintf(stdout, "phase     %s %s\n", summary.Phase, summary.Outcome)
	fmt.Fprintf(stdout, "task      %s\n", summary.TaskID)
	fmt.Fprintf(stdout, "target    %s\n", summary.TargetID)
	fmt.Fprintf(stdout, "created   %s\n", summary.CreatedAt)
	if summary.StartedAt != "" {
		fmt.Fprintf(stdout, "started   %s\n", summary.StartedAt)
	}
	if summary.FinishedAt != "" {
		fmt.Fprintf(stdout, "finished  %s\n", summary.FinishedAt)
	}
	if summary.Error != "" {
		fmt.Fprintf(stdout, "error     %s\n", summary.Error)
	}
	fmt.Fprintf(stdout, "board     claims=%d intents=%d attempts=%d artifacts=%d\n", summary.Claims, summary.Intents, summary.Attempts, summary.Artifacts)
	if summary.DroppedLiveEvents > 0 {
		fmt.Fprintf(stdout, "stream    dropped_live_events=%d (records remain in the store)\n", summary.DroppedLiveEvents)
	}
	return nil
}

// runsEvents implements `harness runs events`.
func runsEvents(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("runs events", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", ".harness/harness.db", "database")
	id := fs.String("id", "", "run id")
	after := fs.Int64("after", 0, "after sequence")
	if err := fs.Parse(args); err != nil {
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
}

// runsLogs implements `harness runs logs --tail`.
func runsLogs(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("runs logs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", ".harness/harness.db", "database")
	id := fs.String("id", "", "run id")
	after := fs.Int64("after", 0, "after sequence")
	tail := fs.Int("tail", 0, "print only the last N records")
	if err := fs.Parse(args); err != nil {
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
	logs, err := store.ListLogs(context.Background(), *id, *after)
	if err != nil {
		return err
	}
	if *tail > 0 && len(logs) > *tail {
		logs = logs[len(logs)-*tail:]
	}
	for _, record := range logs {
		fmt.Fprintf(stdout, "%s %s %s\n", record.Time.Format("2006-01-02T15:04:05Z"), record.SessionID, record.Text)
	}
	return nil
}

// runsBoard implements `harness runs board`.
func runsBoard(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("runs board", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", ".harness/harness.db", "database")
	id := fs.String("id", "", "run id")
	if err := fs.Parse(args); err != nil {
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
	ctx := context.Background()
	claims, err := store.ListClaims(ctx, *id)
	if err != nil {
		return err
	}
	intents, err := store.ListIntents(ctx, *id)
	if err != nil {
		return err
	}
	attempts, err := store.ListIntentAttempts(ctx, *id)
	if err != nil {
		return err
	}
	observations, err := store.ListBlackboardObservations(ctx, *id)
	if err != nil {
		return err
	}
	return printJSON(stdout, map[string]any{
		"run_id":       *id,
		"claims":       claims,
		"intents":      intents,
		"attempts":     attempts,
		"observations": observations,
	})
}

// runsArtifacts implements `harness runs artifacts`.
func runsArtifacts(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("runs artifacts", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", ".harness/harness.db", "database")
	id := fs.String("id", "", "run id")
	if err := fs.Parse(args); err != nil {
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
	artifacts, err := store.ListArtifacts(context.Background(), *id)
	if err != nil {
		return err
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Name < artifacts[j].Name })
	return printJSON(stdout, artifacts)
}
