package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/red-copilot/harness"
	"github.com/red-copilot/harness/verify"
)

type TargetRequest struct {
	Kind        string   `json:"kind,omitempty"`
	ID          string   `json:"id,omitempty"`
	Addresses   []string `json:"addresses,omitempty"`
	Image       string   `json:"image,omitempty"`
	Command     []string `json:"command,omitempty"`
	Ports       []string `json:"ports,omitempty"`
	Network     string   `json:"network,omitempty"`
	ComposeFile string   `json:"compose_file,omitempty"`
	ProjectName string   `json:"project_name,omitempty"`
	Service     string   `json:"service,omitempty"`
	Port        string   `json:"port,omitempty"`
}

type RunRequest struct {
	Run             harness.RunSpec `json:"run"`
	VerifierCommand *verify.Command `json:"verifier_command,omitempty"`
	TSec            *TSecRequest    `json:"tsec,omitempty"`
	Target          *TargetRequest  `json:"target,omitempty"`
}

type TSecRequest struct {
	BaseURL   string `json:"base_url"`
	Token     string `json:"token,omitempty"`
	Challenge string `json:"challenge"`
	UseHint   bool   `json:"use_hint,omitempty"`
}

type Builder func(RunRequest) (harness.RunSpec, error)

type SweepRequest struct {
	Run        harness.RunSpec `json:"run"`
	TSec       *TSecRequest    `json:"tsec,omitempty"`
	Challenges []string        `json:"challenges,omitempty"`
	Parallel   int             `json:"parallel,omitempty"`
	UseHint    bool            `json:"use_hint,omitempty"`
}

type SweepPlan struct {
	ChallengeCodes []string
	Parallel       int
	Build          func(string) (harness.RunSpec, error)
}

type SweepBuilder func(SweepRequest) (SweepPlan, error)

type SweepState struct {
	ID         string           `json:"id"`
	Status     harness.Status   `json:"status"`
	RunIDs     []string         `json:"run_ids,omitempty"`
	Results    []harness.Result `json:"results,omitempty"`
	Error      string           `json:"error,omitempty"`
	CreatedAt  time.Time        `json:"created_at"`
	FinishedAt *time.Time       `json:"finished_at,omitempty"`
}

type Server struct {
	runner       *harness.Runner
	store        harness.Store
	builder      Builder
	sweepBuilder SweepBuilder

	mu     sync.Mutex
	live   map[string]*harness.Run
	sweeps map[string]*SweepState
}

func New(runner *harness.Runner, store harness.Store, builder Builder, sweepBuilder ...SweepBuilder) *Server {
	s := &Server{runner: runner, store: store, builder: builder,
		live: map[string]*harness.Run{}, sweeps: map[string]*SweepState{}}
	if len(sweepBuilder) > 0 {
		s.sweepBuilder = sweepBuilder[0]
	}
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/v1/runs", s.runs)
	mux.HandleFunc("/v1/runs/", s.run)
	mux.HandleFunc("/v1/tsec/sweeps", s.sweepListCreate)
	mux.HandleFunc("/v1/tsec/sweeps/", s.sweepGet)
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
	case http.MethodPost:
		s.createRun(w, r)
	case http.MethodGet:
		runs, err := s.store.ListRuns(r.Context(), 100)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, runs)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) createRun(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var request RunRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid run request: "+err.Error())
		return
	}
	spec := request.Run
	if s.builder != nil {
		var err error
		spec, err = s.builder(request)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
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
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		s.streamEvents(w, r, runID)
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
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
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

func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request, runID string) {
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
	if value := r.URL.Query().Get("after_seq"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "after_seq must be a non-negative integer")
			return
		}
		after = parsed
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	lastSeq := after
	for {
		events, err := s.store.ListEvents(r.Context(), runID, lastSeq)
		if err != nil {
			return
		}
		for _, event := range events {
			data, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Seq, event.Type, data)
			lastSeq = event.Seq
		}
		flusher.Flush()
		record, err := s.store.GetRun(r.Context(), runID)
		if err != nil {
			return
		}
		if isTerminal(record.Status) && len(events) == 0 {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (s *Server) lookupLive(runID string) *harness.Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live[runID]
}

func isTerminal(status harness.Status) bool {
	switch status {
	case harness.StatusSucceeded, harness.StatusFailed, harness.StatusCancelled,
		harness.StatusTimedOut, harness.StatusInterrupted:
		return true
	default:
		return false
	}
}

func (s *Server) sweepListCreate(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.createSweep(w, r)
	case http.MethodGet:
		s.mu.Lock()
		out := make([]SweepState, 0, len(s.sweeps))
		for _, sweep := range s.sweeps {
			out = append(out, snapshotSweepLocked(sweep))
		}
		s.mu.Unlock()
		if len(out) == 0 {
			sweeps, err := s.store.ListSweeps(r.Context(), 100)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			for _, record := range sweeps {
				var state SweepState
				if json.Unmarshal(record.Result, &state) == nil {
					out = append(out, state)
				}
			}
		}
		writeJSON(w, http.StatusOK, out)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) createSweep(w http.ResponseWriter, r *http.Request) {
	if s.sweepBuilder == nil {
		writeError(w, http.StatusNotImplemented, "TSec sweeps are not configured")
		return
	}
	defer r.Body.Close()
	var request SweepRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid sweep request: "+err.Error())
		return
	}
	plan, err := s.sweepBuilder(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if plan.Parallel <= 0 {
		plan.Parallel = 3
	}
	if plan.Parallel > 3 {
		plan.Parallel = 3
	}
	state := &SweepState{ID: "sweep_" + time.Now().UTC().Format("20060102T150405.000000000Z"), Status: harness.StatusQueued, CreatedAt: time.Now().UTC()}
	s.mu.Lock()
	s.sweeps[state.ID] = state
	snapshot := snapshotSweepLocked(state)
	s.mu.Unlock()
	s.persistSweep(snapshot)
	go s.executeSweep(state, plan)
	writeJSON(w, http.StatusAccepted, snapshot)
}

func (s *Server) executeSweep(state *SweepState, plan SweepPlan) {
	s.updateSweep(state, func() { state.Status = harness.StatusRunning })
	sem := make(chan struct{}, plan.Parallel)
	var wg sync.WaitGroup
	for _, code := range plan.ChallengeCodes {
		code := code
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			spec, err := plan.Build(code)
			if err != nil {
				s.appendSweepError(state, err)
				return
			}
			run, err := s.runner.Start(context.WithoutCancel(context.Background()), spec)
			if err != nil {
				s.appendSweepError(state, err)
				return
			}
			s.mu.Lock()
			state.RunIDs = append(state.RunIDs, run.ID)
			s.mu.Unlock()
			result, _ := run.Wait(context.Background())
			s.mu.Lock()
			state.Results = append(state.Results, result)
			s.mu.Unlock()
		}()
	}
	wg.Wait()
	s.updateSweep(state, func() {
		finished := time.Now().UTC()
		state.FinishedAt = &finished
		state.Status = harness.StatusSucceeded
		if state.Error != "" || len(state.Results) == 0 {
			state.Status = harness.StatusFailed
		}
	})
}

func (s *Server) updateSweep(state *SweepState, mutate func()) {
	s.mu.Lock()
	mutate()
	snapshot := snapshotSweepLocked(state)
	s.mu.Unlock()
	s.persistSweep(snapshot)
}

func (s *Server) appendSweepError(state *SweepState, err error) {
	s.updateSweep(state, func() {
		if state.Error != "" {
			state.Error += "; "
		}
		state.Error += err.Error()
	})
}

func (s *Server) persistSweep(snapshot SweepState) {
	result, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	_ = s.store.SaveSweep(context.Background(), harness.SweepRecord{
		ID: snapshot.ID, Status: snapshot.Status, Result: result,
		Error: snapshot.Error, CreatedAt: snapshot.CreatedAt, FinishedAt: snapshot.FinishedAt,
	})
}

func (s *Server) sweepGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/tsec/sweeps/")
	s.mu.Lock()
	state := s.sweeps[id]
	var snapshot SweepState
	if state != nil {
		snapshot = snapshotSweepLocked(state)
	}
	s.mu.Unlock()
	if state == nil {
		record, err := s.store.GetSweep(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, "sweep not found")
			return
		}
		if err := json.Unmarshal(record.Result, &snapshot); err != nil {
			writeError(w, http.StatusInternalServerError, "stored sweep is invalid")
			return
		}
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func snapshotSweepLocked(state *SweepState) SweepState {
	snapshot := *state
	snapshot.RunIDs = append([]string(nil), state.RunIDs...)
	snapshot.Results = append([]harness.Result(nil), state.Results...)
	if state.FinishedAt != nil {
		finished := *state.FinishedAt
		snapshot.FinishedAt = &finished
	}
	return snapshot
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func methodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}
