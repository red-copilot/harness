package tsec_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/tsec"
)

func TestClientUsesBenchmarkTokenAndParsesChallengeList(t *testing.T) {
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("BENCHMARK_TOKEN")
		if r.URL.Path != "/openapi/v1/challenges" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		writeJSON(w, []map[string]any{{
			"unique_code": "web_1", "description": "demo", "difficulty": "easy", "level": 1,
			"total_score": 100, "flag_count": 2, "correct_flag_count": 1,
			"is_completed": false, "container_status": "available", "container_addr": []string{"10.0.0.1:8080"},
		}})
	}))
	defer server.Close()
	client, err := tsec.New(tsec.Config{BaseURL: server.URL, Token: "secret-token", CheckVPN: false})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	challenges, err := client.ListChallenges(context.Background())
	if err != nil {
		t.Fatalf("ListChallenges: %v", err)
	}
	if auth != "secret-token" {
		t.Fatalf("auth header = %q", auth)
	}
	if len(challenges) != 1 || challenges[0].UniqueCode != "web_1" || challenges[0].FlagCount != 2 {
		t.Fatalf("unexpected challenges: %#v", challenges)
	}
}

func TestClientLifecycleAndDuplicateErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openapi/v1/challenges/start":
			if r.URL.Query().Get("unique_code") != "web_1" {
				t.Fatalf("unique_code = %q", r.URL.Query().Get("unique_code"))
			}
			writeJSON(w, map[string]any{"unique_code": "web_1", "container_addr": []string{"10.0.0.1:8080"}})
		case "/openapi/v1/challenges/hint":
			writeJSON(w, map[string]any{"unique_code": "web_1", "hint": "try SQLi"})
		case "/openapi/v1/challenges/submit":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			writeJSON(w, map[string]any{"code": "duplicate", "message": "already submitted", "detail": map[string]any{}})
		case "/openapi/v1/challenges/close":
			writeJSON(w, map[string]any{"unique_code": "web_1", "closed": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := tsec.New(tsec.Config{BaseURL: server.URL, Token: "secret-token", CheckVPN: false})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	started, err := client.StartChallenge(context.Background(), "web_1")
	if err != nil {
		t.Fatalf("StartChallenge: %v", err)
	}
	if len(started.ContainerAddr) != 1 {
		t.Fatalf("addresses = %#v", started.ContainerAddr)
	}
	hint, err := client.GetHint(context.Background(), "web_1")
	if err != nil || hint.Hint != "try SQLi" {
		t.Fatalf("hint = %#v, err = %v", hint, err)
	}
	_, err = client.SubmitFlag(context.Background(), "web_1", "flag{x}")
	if err == nil || !tsec.IsCode(err, "duplicate") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
	closed, err := client.CloseChallenge(context.Background(), "web_1")
	if err != nil || !closed.Closed {
		t.Fatalf("close = %#v, err = %v", closed, err)
	}
}

func TestVPNCheckAndRateLimitRetry(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/vpn" {
			writeJSON(w, map[string]any{"status": "ok", "client_ip": "10.0.0.2", "time": "now"})
			return
		}
		if r.URL.Path == "/openapi/v1/challenges" {
			if attempts.Add(1) == 1 {
				writeJSONStatus(w, http.StatusTooManyRequests, map[string]any{"code": "internal_error", "message": "slow down"})
				return
			}
			writeJSON(w, []map[string]any{})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	client, err := tsec.New(tsec.Config{
		BaseURL: server.URL, Token: "secret-token", CheckVPN: true,
		HealthcheckURL: server.URL + "/vpn", RetryDelayMS: 1,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := client.CheckVPN(context.Background())
	if err != nil || !result.OK {
		t.Fatalf("CheckVPN = %#v, err = %v", result, err)
	}
	if _, err := client.ListChallenges(context.Background()); err != nil {
		t.Fatalf("ListChallenges retry: %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
}

func TestBackendProvisionsAndObservesCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openapi/v1/challenges/start":
			writeJSON(w, map[string]any{"unique_code": "web_1", "container_addr": []string{"10.0.0.1:8080"}})
		case "/openapi/v1/challenges":
			writeJSON(w, []map[string]any{{
				"unique_code": "web_1", "description": "demo", "flag_count": 1,
				"correct_flag_count": 1, "is_completed": true, "total_score": 100,
				"container_status": "available", "container_addr": []string{"10.0.0.1:8080"},
			}})
		case "/openapi/v1/challenges/close":
			writeJSON(w, map[string]any{"unique_code": "web_1", "closed": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := tsec.New(tsec.Config{BaseURL: server.URL, Token: "secret-token", CheckVPN: false})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	backend := tsec.NewBackend(client, "web_1")
	backend.CheckVPN = false
	target, err := backend.Provision(context.Background(), harness.ProvisionRequest{})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if target.Runtime == nil || target.Runtime.Network != "host" || target.Runtime.ChallengeCode != "web_1" {
		t.Fatalf("target runtime = %#v", target.Runtime)
	}
	completion, err := backend.Observe(context.Background(), target)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !completion.Done || !completion.Success || completion.Score != 100 {
		t.Fatalf("completion = %#v", completion)
	}
}

func TestBackendReportsPartialFlagProgress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openapi/v1/challenges":
			writeJSON(w, []map[string]any{{
				"unique_code": "b-02", "description": "multi flag", "flag_count": 6,
				"correct_flag_count": 2, "is_completed": false, "total_score": 0,
				"container_status": "available", "container_addr": []string{"10.0.0.1:80"},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := tsec.New(tsec.Config{BaseURL: server.URL, Token: "secret-token", CheckVPN: false})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	backend := tsec.NewBackend(client, "b-02")
	backend.CheckVPN = false
	completion, err := backend.Observe(context.Background(), harness.Target{ID: "b-02"})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if completion.Done || completion.Progress != 2 || completion.Total != 6 {
		t.Fatalf("completion = %#v, want partial 2/6", completion)
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
func writeJSONStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func TestClientRejectsMissingToken(t *testing.T) {
	if _, err := tsec.New(tsec.Config{BaseURL: "http://example.test"}); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("expected token error, got %v", err)
	}
}

func TestClientEnforcesInstanceCap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"unique_code": r.URL.Query().Get("unique_code"), "container_addr": []string{"10.0.0.1:1"}})
	}))
	defer server.Close()
	client, err := tsec.New(tsec.Config{BaseURL: server.URL, Token: "secret-token", CheckVPN: false, InstanceLimit: 3})
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"a", "b", "c"} {
		if _, err := client.StartChallenge(context.Background(), code); err != nil {
			t.Fatalf("start %s: %v", code, err)
		}
	}
	if _, err := client.StartChallenge(context.Background(), "d"); err == nil {
		t.Fatal("expected instance cap error")
	}
	if _, err := client.CloseChallenge(context.Background(), "a"); err != nil {
		t.Fatalf("close a: %v", err)
	}
	if _, err := client.StartChallenge(context.Background(), "d"); err != nil {
		t.Fatalf("start after release: %v", err)
	}
}
