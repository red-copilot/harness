package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/red-copilot/harness/exploration"
	"github.com/red-copilot/harness/server"
	"github.com/red-copilot/harness/store/sqlite"
)

func TestServerExposesBranchesAndTaskKnowledge(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SaveBranch(ctx, exploration.Branch{ID: "branch-1", TaskID: "task-1", RunID: "run-1", Depth: 1, Topic: "web.oa", Status: exploration.BranchOpen}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveKnowledge(ctx, exploration.Knowledge{ID: "k-1", TaskID: "task-1", Topic: "web.oa", Kind: exploration.KnowledgeAnchor, Statement: "OA exists", Scope: exploration.KnowledgeScopeTarget, Trust: exploration.KnowledgeObserved, Reuse: exploration.ReuseStable}); err != nil {
		t.Fatal(err)
	}
	handler := server.New(nil, store, nil).Handler()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/runs/run-1/branches", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("branches status = %d body=%s", rec.Code, rec.Body.String())
	}
	var branches []exploration.Branch
	if err := json.Unmarshal(rec.Body.Bytes(), &branches); err != nil || len(branches) != 1 {
		t.Fatalf("branches = %v %s", err, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/tasks/task-1/knowledge", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("knowledge status = %d body=%s", rec.Code, rec.Body.String())
	}
	var knowledge []exploration.Knowledge
	if err := json.Unmarshal(rec.Body.Bytes(), &knowledge); err != nil || len(knowledge) != 1 {
		t.Fatalf("knowledge = %v %s", err, rec.Body.String())
	}
}
