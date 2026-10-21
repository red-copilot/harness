package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/red-copilot/harness"
)

type RunRequest struct {
	Descriptor harness.RunDescriptor `json:"descriptor"`
}

func (r *RunRequest) UnmarshalJSON(data []byte) error {
	var envelope struct {
		Descriptor json.RawMessage `json:"descriptor"`
	}
	if err := json.Unmarshal(data, &envelope); err == nil && len(envelope.Descriptor) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(envelope.Descriptor))
		decoder.DisallowUnknownFields()
		return decoder.Decode(&r.Descriptor)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(&r.Descriptor)
}

type Builder func(harness.RunDescriptor) (harness.RunSpec, error)

type Server struct {
	runner  *harness.Runner
	store   harness.Store
	builder Builder
	mu      sync.Mutex
	live    map[string]*harness.Run
}

func New(runner *harness.Runner, store harness.Store, builder Builder) *Server {
	return &Server{runner: runner, store: store, builder: builder, live: map[string]*harness.Run{}}
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/v1/runs", s.runs)
	mux.HandleFunc("/v1/runs/", s.run)
	mux.HandleFunc("/v1/tasks/", s.task)
	return mux
}
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
func (s *Server) runs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		records, err := s.store.ListRuns(r.Context(), 100)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, records)
	case http.MethodPost:
		defer r.Body.Close()
		var request RunRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid run request: "+err.Error())
			return
		}
		if err := harness.ValidateDescriptor(request.Descriptor); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		spec, err := s.builder(request.Descriptor)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		run, err := s.runner.Start(context.WithoutCancel(r.Context()), spec)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.mu.Lock()
		s.live[run.ID] = run
		s.mu.Unlock()
		writeJSON(w, http.StatusAccepted, map[string]string{"run_id": run.ID})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}
func (s *Server) run(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/runs/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	runID := parts[0]
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		record, err := s.store.GetRun(r.Context(), runID)
		if err != nil {
			writeError(w, http.StatusNotFound, "run not found")
			return
		}
		writeJSON(w, http.StatusOK, record)
		return
	}
	switch parts[1] {
	case "events":
		s.streamEvents(w, r, runID)
	case "logs":
		s.logs(w, r, runID)
	case "verdict":
		s.verdict(w, r, runID)
	case "branches":
		listRunItems(w, r, runID, s.store.ListBranches)
	case "attempts":
		listRunItems(w, r, runID, s.store.ListAttempts)
	case "observations":
		listRunItems(w, r, runID, s.store.ListObservations)
	case "artifacts":
		listRunItems(w, r, runID, s.store.ListArtifacts)
	case "cancel":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		live := s.lookupLive(runID)
		if live == nil {
			writeError(w, http.StatusConflict, "run is not live")
			return
		}
		if err := live.Cancel(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "cancelled"})
	case "steer":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var body struct {
			SessionID string `json:"session_id"`
			Message   string `json:"message"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid steer request")
			return
		}
		live := s.lookupLive(runID)
		if live == nil {
			writeError(w, http.StatusConflict, "run is not live")
			return
		}
		if err := live.Steer(r.Context(), body.SessionID, body.Message); err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
	default:
		http.NotFound(w, r)
	}
}
func (s *Server) task(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/tasks/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 || parts[1] != "knowledge" {
		http.NotFound(w, r)
		return
	}
	items, err := s.store.QueryKnowledge(r.Context(), harness.KnowledgeQuery{TaskID: parts[0], AllowCrossRun: true, Now: time.Now().UTC()})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}
func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if _, err := s.store.GetRun(r.Context(), runID); err != nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	after := int64(0)
	if value := r.Header.Get("Last-Event-ID"); value != "" {
		after, _ = strconv.ParseInt(value, 10, 64)
	}
	if value := r.URL.Query().Get("after_seq"); value != "" {
		candidate, _ := strconv.ParseInt(value, 10, 64)
		if candidate > after {
			after = candidate
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	writeEvents := func(events []harness.Event) {
		for _, event := range events {
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", event.Seq, data)
			flusher.Flush()
			after = event.Seq
		}
	}
	events, err := s.store.ListEvents(r.Context(), runID, after)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeEvents(events)
	live := s.lookupLive(runID)
	if live == nil {
		return
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
		events, err := s.store.ListEvents(r.Context(), runID, after)
		if err != nil {
			return
		}
		writeEvents(events)
		select {
		case <-live.Done():
			remaining, _ := s.store.ListEvents(r.Context(), runID, after)
			writeEvents(remaining)
			return
		default:
		}
	}
}
func (s *Server) logs(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after_seq"), 10, 64)
	items, err := s.store.ListLogs(r.Context(), runID, after)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}
func (s *Server) verdict(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	verdict, err := s.store.GetVerdict(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusNotFound, "verdict not found")
		return
	}
	writeJSON(w, http.StatusOK, verdict)
}
func (s *Server) lookupLive(runID string) *harness.Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.live[runID]
	if run != nil {
		select {
		case <-run.Done():
			delete(s.live, runID)
			run = nil
		default:
		}
	}
	return run
}
func listRunItems[T any](w http.ResponseWriter, r *http.Request, runID string, list func(context.Context, string) ([]T, error)) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	items, err := list(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func methodNotAllowed(w http.ResponseWriter, allowed ...string) {
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}
