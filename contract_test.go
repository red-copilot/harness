package harness_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/red-copilot/harness"
)

func TestRunDescriptorRoundTrip(t *testing.T) {
	t.Setenv("TEST_SECRET", "super-secret-value")
	descriptor := harness.RunDescriptor{
		SchemaVersion: 1,
		Task:          harness.Task{ID: "task-1", Objective: "authorized test"},
		Target:        harness.Target{ID: "target-1", Addresses: []string{"10.0.0.1:80"}, Isolation: harness.IsolationCallerManaged},
		Planner:       harness.AgentSpec{ID: "planner", Image: "pi-kali"},
		Executors:     []harness.AgentSpec{{ID: "exec-1"}, {ID: "exec-2"}},
		Policy:        harness.DefaultExplorationPolicy(),
		Budget:        harness.Budget{WallTime: time.Minute},
	}
	data, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Runtime") {
		t.Fatalf("runtime interface leaked into descriptor: %s", data)
	}
	var got harness.RunDescriptor
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Task.ID != descriptor.Task.ID || got.Target.ID != descriptor.Target.ID || len(got.Executors) != 2 || got.Policy.MaxDepth != 4 {
		t.Fatalf("round trip mismatch: %#v", got)
	}
}

func TestValidateRunSpecRequiresTaskAndTarget(t *testing.T) {
	cases := []struct {
		name string
		edit func(*harness.RunSpec)
		want string
	}{
		{"task id", func(spec *harness.RunSpec) { spec.Task.ID = "" }, "task.id"},
		{"task objective", func(spec *harness.RunSpec) { spec.Task.Objective = "" }, "task.objective"},
		{"target id", func(spec *harness.RunSpec) { spec.Target.ID = "" }, "target.id"},
		{"isolation", func(spec *harness.RunSpec) { spec.Target.Isolation = "network_isolated" }, "target.isolation"},
		{"executor", func(spec *harness.RunSpec) { spec.Executors = nil }, "executors"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := validRunSpec()
			tc.edit(&spec)
			err := harness.ValidateRunSpec(spec)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want field %q", err, tc.want)
			}
		})
	}
}

func TestValidateRunSpecRequiresRuntimeAndArtifacts(t *testing.T) {
	cases := []struct {
		name string
		edit func(*harness.RunSpec)
		want string
	}{
		{"planner runtime", func(spec *harness.RunSpec) { spec.Planner.Runtime = nil }, "planner.runtime"},
		{"executor runtime", func(spec *harness.RunSpec) { spec.Executors[0].Runtime = nil }, "executors[0].runtime"},
		{"executor id", func(spec *harness.RunSpec) { spec.Executors[0].ID = "" }, "executors[0].id"},
		{"artifacts", func(spec *harness.RunSpec) { spec.Artifacts = nil }, "artifacts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := validRunSpec()
			tc.edit(&spec)
			err := harness.ValidateRunSpec(spec)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want field %q", err, tc.want)
			}
		})
	}
}

func TestValidateRunSpecRequiresAuthority(t *testing.T) {
	spec := validRunSpec()
	spec.Verifier = nil
	spec.Backend = nil
	err := harness.ValidateRunSpec(spec)
	if err == nil || !strings.Contains(err.Error(), "authority") {
		t.Fatalf("err = %v, want authority", err)
	}
}

func TestDefaultExplorationPolicy(t *testing.T) {
	p := harness.DefaultExplorationPolicy()
	if p.MaxDepth != 4 || p.MaxNodes != 15 || p.MaxChildrenPerBranch != 2 || p.MaxActiveBranches != 2 ||
		p.MaxAttemptsPerBranch != 3 || p.MaxToolCallsPerAttempt != 20 || p.MaxWallTimePerAttempt != 10*time.Minute ||
		p.StagnationRounds != 3 || p.MaxPlannerTools != 4 || p.MaxPlannerWallTime != 2*time.Minute ||
		p.MaxKnowledgeItems != 200 || p.MaxKnowledgeStatementBytes != 200 || p.MaxKnowledgeDigestItems != 8 ||
		p.MaxKnowledgeDigestBytes != 2048 || p.MaxReportBytes != 1<<20 || p.MaxEvidenceFilesPerAttempt != 16 ||
		p.MaxArtifactBytes != 10<<20 || p.MaxAttemptArtifactBytes != 50<<20 {
		t.Fatalf("unexpected defaults: %#v", p)
	}
}

func TestKnowledgeTrustHasNoVerified(t *testing.T) {
	values := []harness.KnowledgeTrust{harness.KnowledgeObserved, harness.KnowledgeCorroborated, harness.KnowledgeContested, harness.KnowledgeRefuted}
	seen := map[string]bool{}
	for _, value := range values {
		seen[string(value)] = true
	}
	if seen["verified"] {
		t.Fatal("verified knowledge trust must not exist")
	}
}

func validRunSpec() harness.RunSpec {
	factory := &fakeRuntime{}
	return harness.RunSpec{
		Task:      harness.Task{ID: "task-1", Objective: "authorized test"},
		Target:    harness.Target{ID: "target-1", Addresses: []string{"10.0.0.1:80"}, Isolation: harness.IsolationCallerManaged},
		Planner:   harness.AgentSpec{ID: "planner", Runtime: factory},
		Executors: []harness.AgentSpec{{ID: "exec-1", Runtime: factory}},
		Policy:    harness.DefaultExplorationPolicy(),
		Verifier:  fakeVerifier{verdict: harness.Verdict{Success: true}},
		Artifacts: &fakeArtifactStore{},
	}
}

type fakeRuntime struct {
	mu       sync.Mutex
	requests []harness.RuntimeRequest
	new      func(harness.RuntimeRequest) harness.AgentSession
}

func (f *fakeRuntime) NewSession(_ context.Context, req harness.RuntimeRequest) (harness.AgentSession, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	if f.new != nil {
		return f.new(req), nil
	}
	return &fakeSession{events: make(chan harness.AgentEvent, 4)}, nil
}

type fakeVerifier struct {
	verdict harness.Verdict
	err     error
}

func (f fakeVerifier) Verify(context.Context, harness.VerifyRequest) (harness.Verdict, error) {
	return f.verdict, f.err
}

type fakeArtifactStore struct {
	mu      sync.Mutex
	puts    int
	fail    error
	content map[string][]byte
}

func (f *fakeArtifactStore) Put(_ context.Context, in harness.ArtifactInput) (harness.Artifact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return harness.Artifact{}, f.fail
	}
	data, err := ioReadAllLimit(in.Content, in.Size+1)
	if err != nil {
		return harness.Artifact{}, err
	}
	if int64(len(data)) != in.Size {
		return harness.Artifact{}, errors.New("artifact size mismatch")
	}
	sum := sha256Hex(data)
	if f.content == nil {
		f.content = map[string][]byte{}
	}
	f.content[sum] = append([]byte(nil), data...)
	f.puts++
	return harness.Artifact{ID: sum, RunID: in.RunID, AttemptID: in.AttemptID, SessionID: in.SessionID, Name: in.Name, MediaType: in.MediaType, Size: int64(len(data)), SHA256: sum}, nil
}

func (f *fakeArtifactStore) Open(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}

func TestUnknownSchemaVersionRejected(t *testing.T) {
	for _, version := range []int{0, 2, 999} {
		if err := harness.ValidateDescriptor(harness.RunDescriptor{SchemaVersion: version}); err == nil || !strings.Contains(err.Error(), "schema_version") {
			t.Fatalf("version %d err = %v, want schema_version error", version, err)
		}
	}
	if err := harness.ValidateDescriptor(harness.RunDescriptor{SchemaVersion: harness.CurrentSchemaVersion}); err != nil {
		t.Fatal(err)
	}
}

// TestStorePortIsComposedOfPlanes forces every persistence method into exactly
// one plane, so Store cannot grow back into an unpartitioned god interface.
func TestStorePortIsComposedOfPlanes(t *testing.T) {
	planes := map[string]reflect.Type{
		"RunStore":         reflect.TypeOf((*harness.RunStore)(nil)).Elem(),
		"AuditStore":       reflect.TypeOf((*harness.AuditStore)(nil)).Elem(),
		"ExplorationStore": reflect.TypeOf((*harness.ExplorationStore)(nil)).Elem(),
		"KnowledgeStore":   reflect.TypeOf((*harness.KnowledgeStore)(nil)).Elem(),
	}
	owner := map[string]string{}
	for name, plane := range planes {
		if plane.NumMethod() == 0 {
			t.Fatalf("plane %s is empty", name)
		}
		for i := 0; i < plane.NumMethod(); i++ {
			method := plane.Method(i).Name
			if previous, ok := owner[method]; ok {
				t.Fatalf("method %s belongs to both %s and %s", method, previous, name)
			}
			owner[method] = name
		}
	}
	storePort := reflect.TypeOf((*harness.Store)(nil)).Elem()
	for i := 0; i < storePort.NumMethod(); i++ {
		method := storePort.Method(i).Name
		if method == "Close" {
			continue
		}
		if _, ok := owner[method]; !ok {
			t.Fatalf("Store method %s belongs to no plane", method)
		}
	}
	if _, ok := reflect.TypeOf(harness.VerifyRequest{}).FieldByName("Store"); ok {
		t.Fatal("VerifyRequest grants the verifier persistence access")
	}
}

// TestStorePortHasNoCampaignSurface keeps the SDK single-Run/single-Target:
// the persistence port must not expose campaign-wide primitives.
func TestStorePortHasNoCampaignSurface(t *testing.T) {
	storePort := reflect.TypeOf((*harness.Store)(nil)).Elem()
	for i := 0; i < storePort.NumMethod(); i++ {
		if strings.Contains(strings.ToLower(storePort.Method(i).Name), "sweep") {
			t.Fatalf("Store exposes campaign method %q", storePort.Method(i).Name)
		}
	}
	if _, ok := reflect.TypeOf(harness.RunRecord{}).FieldByName("SweepID"); ok {
		t.Fatal("RunRecord carries a campaign identifier")
	}
}
