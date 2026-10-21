package harness_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/store/sqlite"
	"github.com/red-copilot/harness/tsec"
)

func TestEndToEndPlannerExecutorEvidenceKnowledgeVerdict(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runner, err := harness.NewRunner(harness.Config{Store: store, Starter: noopStarter{}, WorkspaceRoot: t.TempDir(), SkipRecovery: true})
	if err != nil {
		t.Fatal(err)
	}
	spec := acceptanceSpec(store)
	spec.PlannerStrategy = staticPlannerStrategy{proposal: harness.PlanProposal{CreateBranches: []harness.BranchProposal{
		{ParentID: "root", Topic: "web.one", Goal: "one", Hypothesis: "one", Target: "target-1", SuccessCondition: "yes", FailureCondition: "no"},
		{ParentID: "root", Topic: "web.two", Goal: "two", Hypothesis: "two", Target: "target-1", SuccessCondition: "yes", FailureCondition: "no"},
	}}}
	spec.ExecutorStrategy = knowledgeExecutorStrategy{}
	result, err := runner.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != harness.StatusSucceeded || result.Verdict == nil || !result.Verdict.Success {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Branches) < 3 || len(result.Attempts) < 2 || len(result.Observations) < 2 || len(result.Artifacts) < 2 || len(result.Knowledge) < 2 {
		t.Fatalf("incomplete result: branches=%d attempts=%d observations=%d artifacts=%d knowledge=%d", len(result.Branches), len(result.Attempts), len(result.Observations), len(result.Artifacts), len(result.Knowledge))
	}
}

func TestMalformedExecutorReportRetriesThenBlocks(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runner, err := harness.NewRunner(harness.Config{Store: store, Starter: noopStarter{}, WorkspaceRoot: t.TempDir(), SkipRecovery: true})
	if err != nil {
		t.Fatal(err)
	}
	spec := acceptanceSpec(store)
	spec.Verifier = fakeVerifier{verdict: harness.Verdict{Success: false}}
	spec.ExecutorStrategy = malformedExecutorStrategy{}
	result, err := runner.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status == harness.StatusSucceeded {
		t.Fatal("malformed report succeeded")
	}
	if len(result.Attempts) != 3 {
		t.Fatalf("attempts = %d, want 3", len(result.Attempts))
	}
	for _, branch := range result.Branches {
		if branch.Kind == harness.BranchHypothesis && branch.Status != harness.BranchBlocked {
			t.Fatalf("branch = %+v", branch)
		}
	}
}

func TestBackendSuccessRacesInflightAttempt(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runner, err := harness.NewRunner(harness.Config{Store: store, Starter: noopStarter{}, WorkspaceRoot: t.TempDir(), SkipRecovery: true, PollInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	spec := acceptanceSpec(store)
	spec.Verifier = nil
	attachBackend(&spec, &fakeBackend{completion: harness.Completion{Done: true, Success: true, Score: 11}})
	spec.Planner.Runtime = &fakeRuntime{new: func(harness.RuntimeRequest) harness.AgentSession {
		return &fakeSession{events: make(chan harness.AgentEvent), prompt: func(ctx context.Context, _ string) error { <-ctx.Done(); return ctx.Err() }}
	}}
	result, err := runner.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != harness.StatusSucceeded || result.Verdict == nil || result.Verdict.Score != 11 {
		t.Fatalf("result = %#v", result)
	}
}

func TestSQLiteContainsNoKnownSecrets(t *testing.T) {
	secret := "super-secret-value"
	t.Setenv("TEST_SECRET", secret)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "harness.db")
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runner, err := harness.NewRunner(harness.Config{Store: store, Starter: noopStarter{}, WorkspaceRoot: filepath.Join(dir, "workspaces"), SkipRecovery: true})
	if err != nil {
		t.Fatal(err)
	}
	spec := acceptanceSpec(store)
	spec.SecretEnv = []string{"TEST_SECRET"}
	spec.PlannerStrategy = staticPlannerStrategy{proposal: harness.PlanProposal{CreateBranches: []harness.BranchProposal{{ParentID: "root", Topic: "web." + secret, Goal: secret, Hypothesis: secret, Target: "target-1", SuccessCondition: secret, FailureCondition: secret}}}}
	spec.ExecutorStrategy = secretExecutorStrategy{secret: secret}
	spec.Verifier = fakeVerifier{verdict: harness.Verdict{Success: true, Message: secret, Evidence: json.RawMessage(`{"secret":"` + secret + `"}`)}}
	result, err := runner.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprint(result), secret) {
		t.Fatalf("secret leaked in result: %+v", result)
	}
	assertSecretAbsentFromStore(t, dbPath, filepath.Join(dir, "artifacts"), secret)
}

// TestTSecTokenNeverPersisted proves the documented BENCHMARK_TOKEN boundary:
// the token authenticates the challenge API and may reach the agent, but no
// canonical table, artifact or result may contain it in plaintext.
func TestTSecTokenNeverPersisted(t *testing.T) {
	secret := "tsec-token-8f4d2b7c"
	t.Setenv("BENCHMARK_TOKEN", secret)
	t.Setenv("BENCHMARK_BASE_URL", "https://tsec.example")
	var authSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("BENCHMARK_TOKEN") == secret {
			authSeen = true
		}
		switch r.URL.Path {
		case "/openapi/v1/challenges/start":
			writeJSONBody(w, map[string]any{"unique_code": "web-1", "container_addr": []string{"10.0.0.1:8080"}})
		case "/openapi/v1/challenges":
			writeJSONBody(w, []map[string]any{{
				"unique_code": "web-1", "description": "demo", "flag_count": 1, "correct_flag_count": 1,
				"is_completed": true, "total_score": 100, "container_status": "available",
				"container_addr": []string{"10.0.0.1:8080"},
			}})
		case "/openapi/v1/challenges/close":
			writeJSONBody(w, map[string]any{"unique_code": "web-1", "closed": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := tsec.New(tsec.Config{BaseURL: server.URL, Token: secret, CheckVPN: false})
	if err != nil {
		t.Fatal(err)
	}
	backend := tsec.NewBackend(client, "web-1")
	backend.CheckVPN = false
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "harness.db")
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runner, err := harness.NewRunner(harness.Config{Store: store, Starter: noopStarter{}, WorkspaceRoot: filepath.Join(dir, "workspaces"), PollInterval: 10 * time.Millisecond, SkipRecovery: true})
	if err != nil {
		t.Fatal(err)
	}
	spec := acceptanceSpec(store)
	spec.Verifier = nil
	attachBackend(&spec, backend)
	spec.Target = harness.Target{ID: "web-1", Isolation: harness.IsolationCallerManaged}
	spec.SecretEnv = []string{"BENCHMARK_TOKEN"}
	spec.Executors[0].EnvFrom = []string{"BENCHMARK_TOKEN", "BENCHMARK_BASE_URL"}
	result, err := runner.Run(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !authSeen {
		t.Fatal("challenge API never received the token")
	}
	if result.Status != harness.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", result.Status)
	}
	if strings.Contains(fmt.Sprint(result), secret) {
		t.Fatalf("token leaked in result: %+v", result)
	}
	assertSecretAbsentFromStore(t, dbPath, filepath.Join(dir, "artifacts"), secret)
}

func assertSecretAbsentFromStore(t *testing.T, dbPath, artifactRoot, secret string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	_ = rows.Close()
	for _, table := range tables {
		data, err := db.Query(`SELECT * FROM "` + table + `"`)
		if err != nil {
			t.Fatal(err)
		}
		columns, _ := data.Columns()
		for data.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := data.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(fmt.Sprint(values...), secret) {
				_ = data.Close()
				t.Fatalf("secret in table %s: %v", table, values)
			}
		}
		_ = data.Close()
	}
	err = filepath.WalkDir(artifactRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), secret) {
			t.Fatalf("secret in artifact %s", path)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func writeJSONBody(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func acceptanceSpec(store harness.ArtifactStore) harness.RunSpec {
	return harness.RunSpec{Task: harness.Task{ID: "task-1", Objective: "authorized test"}, Target: harness.Target{ID: "target-1", Addresses: []string{"10.0.0.1:80"}, Isolation: harness.IsolationCallerManaged}, Planner: harness.AgentSpec{ID: "planner", Runtime: &fakeRuntime{}}, Executors: []harness.AgentSpec{{ID: "exec-1", Runtime: &fakeRuntime{}}, {ID: "exec-2", Runtime: &fakeRuntime{}}}, Policy: harness.DefaultExplorationPolicy(), Artifacts: store, Verifier: fakeVerifier{verdict: harness.Verdict{Success: true, Score: 1}}, PlannerStrategy: staticPlannerStrategy{proposal: harness.PlanProposal{CreateBranches: []harness.BranchProposal{{ParentID: "root", Topic: "web.initial", Goal: "initial", Hypothesis: "initial", Target: "target-1", SuccessCondition: "yes", FailureCondition: "no"}}}}, ExecutorStrategy: knowledgeExecutorStrategy{}}
}

type knowledgeExecutorStrategy struct{}

func (knowledgeExecutorStrategy) Prompt(harness.AttemptRequest) (string, error) {
	return "execute", nil
}
func (knowledgeExecutorStrategy) Parse(req harness.AttemptRequest, _ harness.TurnResult) (harness.AttemptReport, error) {
	if err := os.WriteFile(filepath.Join(req.WorkspaceDir, "proof.txt"), []byte("evidence"), 0o600); err != nil {
		return harness.AttemptReport{}, err
	}
	return harness.AttemptReport{BranchID: req.Branch.ID, Status: harness.AttemptCompleted, Outcome: harness.AttemptLocalSuccess, Observations: []harness.ObservationProposal{{LocalID: "obs", Kind: "adapter.test", Summary: "observed", Target: "target-1", EvidenceFiles: []string{"proof.txt"}}}, Knowledge: []harness.KnowledgeProposal{{Topic: req.Branch.Topic, Kind: "adapter.test", Statement: "evidence observed", Scope: harness.KnowledgeScopeTask, Target: "target-1", ObservationRefs: []string{"obs"}, Reuse: harness.ReuseStable}}}, nil
}

type malformedExecutorStrategy struct{}

func (malformedExecutorStrategy) Prompt(harness.AttemptRequest) (string, error) {
	return "execute", nil
}
func (malformedExecutorStrategy) Parse(req harness.AttemptRequest, _ harness.TurnResult) (harness.AttemptReport, error) {
	return harness.AttemptReport{BranchID: req.Branch.ID, Status: harness.AttemptCompleted, Outcome: harness.AttemptLocalSuccess, Observations: []harness.ObservationProposal{{LocalID: "obs", Kind: "adapter.test", Summary: "missing evidence", Target: "target-1"}}}, nil
}

type secretExecutorStrategy struct{ secret string }

func (s secretExecutorStrategy) Prompt(harness.AttemptRequest) (string, error) { return "execute", nil }
func (s secretExecutorStrategy) Parse(req harness.AttemptRequest, _ harness.TurnResult) (harness.AttemptReport, error) {
	if err := os.WriteFile(filepath.Join(req.WorkspaceDir, "proof.txt"), []byte("token="+s.secret), 0o600); err != nil {
		return harness.AttemptReport{}, err
	}
	return harness.AttemptReport{BranchID: req.Branch.ID, Status: harness.AttemptCompleted, Outcome: harness.AttemptLocalSuccess, Observations: []harness.ObservationProposal{{LocalID: "obs", Kind: "adapter.test", Summary: "redacted evidence", Target: "target-1", EvidenceFiles: []string{"proof.txt"}}}}, nil
}
