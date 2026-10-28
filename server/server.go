package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
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

type TSecRequest struct {
	BaseURL   string `json:"base_url,omitempty"`
	Token     string `json:"token,omitempty"`
	Challenge string `json:"challenge,omitempty"`
	UseHint   bool   `json:"use_hint,omitempty"`
}

type SweepRequest struct {
	Descriptor harness.RunDescriptor `json:"descriptor"`
	TSec       *TSecRequest          `json:"tsec,omitempty"`
	Challenges []string              `json:"challenges,omitempty"`
	Parallel   int                   `json:"parallel,omitempty"`
	UseHint    bool                  `json:"use_hint,omitempty"`
}

// SweepPlan is batch composition only. Each Build call must return a normal
// v2 RunSpec, so every sweep Run still uses the single canonical engine.
type SweepPlan struct {
	ChallengeCodes []string
	Parallel       int
	Build          func(string) (harness.RunSpec, error)
}

type SweepBuilder func(SweepRequest) (SweepPlan, error)

// SweepStore is a read-only history boundary for completed or in-flight batch
// summaries. The server never uses it as a second Run control writer.
type SweepStore interface {
	SaveSweep(context.Context, harness.SweepRecord) error
	GetSweep(context.Context, string) (harness.SweepRecord, error)
	ListSweeps(context.Context, int) ([]harness.SweepRecord, error)
}

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
	store        harness.ReadStore
	builder      Builder
	sweepBuilder SweepBuilder
	mu           sync.Mutex
	live         map[string]*harness.Run
	sweeps       map[string]*SweepState
}

func New(runner *harness.Runner, store harness.ReadStore, builder Builder, sweepBuilder ...SweepBuilder) *Server {
	s := &Server{runner: runner, store: store, builder: builder, live: map[string]*harness.Run{}, sweeps: map[string]*SweepState{}}
	if len(sweepBuilder) > 0 {
		s.sweepBuilder = sweepBuilder[0]
	}
	return s
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/v1/overview", s.overview)
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

// overviewWindow is how many recent Runs the overview aggregates.
const overviewWindow = 100

type overviewRuns struct {
	Total        int     `json:"total"`
	Active       int     `json:"active"`
	Terminal     int     `json:"terminal"`
	Queued       int     `json:"queued"`
	Provisioning int     `json:"provisioning"`
	Running      int     `json:"running"`
	Finalizing   int     `json:"finalizing"`
	Verifying    int     `json:"verifying"`
	Succeeded    int     `json:"succeeded"`
	Failed       int     `json:"failed"`
	Cancelled    int     `json:"cancelled"`
	TimedOut     int     `json:"timed_out"`
	Interrupted  int     `json:"interrupted"`
	Stagnated    int     `json:"stagnated"`
	SuccessRate  float64 `json:"success_rate"`
}

type overviewDuration struct {
	Samples   int     `json:"samples"`
	AverageMS float64 `json:"average_ms"`
	P50MS     float64 `json:"p50_ms"`
	P95MS     float64 `json:"p95_ms"`
}

type overviewResponse struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Window      int              `json:"window"`
	Runs        overviewRuns     `json:"runs"`
	Usage       harness.Usage    `json:"usage"`
	Duration    overviewDuration `json:"duration"`
}

// overview aggregates the same canonical Run records the UI lists. It is a
// read-only projection; it never derives success from anything but the stored
// terminal status.
func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	records, err := s.store.ListRuns(r.Context(), overviewWindow)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	response := overviewResponse{GeneratedAt: time.Now().UTC(), Window: overviewWindow}
	response.Runs.Total = len(records)
	var durations []float64
	for _, record := range records {
		switch record.Status {
		case harness.StatusQueued:
			response.Runs.Queued++
		case harness.StatusProvisioning:
			response.Runs.Provisioning++
		case harness.StatusRunning:
			response.Runs.Running++
		case harness.StatusFinalizing:
			response.Runs.Finalizing++
		case harness.StatusVerifying:
			response.Runs.Verifying++
		case harness.StatusSucceeded:
			response.Runs.Succeeded++
		case harness.StatusFailed:
			response.Runs.Failed++
		case harness.StatusCancelled:
			response.Runs.Cancelled++
		case harness.StatusTimedOut:
			response.Runs.TimedOut++
		case harness.StatusInterrupted:
			response.Runs.Interrupted++
		case harness.StatusStagnated:
			response.Runs.Stagnated++
		}
		if len(record.Result) == 0 {
			continue
		}
		var result harness.Result
		if err := json.Unmarshal(record.Result, &result); err != nil {
			continue
		}
		response.Usage.InputTokens += result.Usage.InputTokens
		response.Usage.OutputTokens += result.Usage.OutputTokens
		response.Usage.TotalTokens += result.Usage.TotalTokens
		response.Usage.ModelCalls += result.Usage.ModelCalls
		response.Usage.ToolCalls += result.Usage.ToolCalls
		response.Usage.CostUSD += result.Usage.CostUSD
		if result.StartedAt != nil && result.FinishedAt != nil && result.FinishedAt.After(*result.StartedAt) {
			durations = append(durations, float64(result.FinishedAt.Sub(*result.StartedAt).Milliseconds()))
		}
	}
	response.Runs.Active = response.Runs.Queued + response.Runs.Provisioning + response.Runs.Running + response.Runs.Finalizing + response.Runs.Verifying
	response.Runs.Terminal = response.Runs.Total - response.Runs.Active
	if response.Runs.Terminal > 0 {
		response.Runs.SuccessRate = float64(response.Runs.Succeeded) / float64(response.Runs.Terminal)
	}
	response.Duration = summarizeDurations(durations)
	writeJSON(w, http.StatusOK, response)
}

func summarizeDurations(values []float64) overviewDuration {
	if len(values) == 0 {
		return overviewDuration{}
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	var total float64
	for _, value := range sorted {
		total += value
	}
	return overviewDuration{
		Samples:   len(sorted),
		AverageMS: total / float64(len(sorted)),
		P50MS:     nearestRank(sorted, 0.50),
		P95MS:     nearestRank(sorted, 0.95),
	}
}

func nearestRank(sorted []float64, percentile float64) float64 {
	index := int(math.Ceil(percentile*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
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
	case "artifacts":
		listRunItems(w, r, runID, s.store.ListArtifacts)
	case "graph":
		s.graph(w, r, runID)
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

type graphReason struct {
	Active          bool   `json:"active"`
	FrontierVersion uint64 `json:"frontier_version"`
	FreeSlots       []int  `json:"free_slots,omitempty"`
}

type graphResponse struct {
	Facts         []harness.Fact          `json:"facts"`
	Intents       []harness.Intent        `json:"intents"`
	Hints         []harness.Hint          `json:"hints"`
	Attempts      []harness.IntentAttempt `json:"attempts"`
	Slots         []int                   `json:"slots"`
	BoardRevision uint64                  `json:"board_revision"`
	Reason        graphReason             `json:"reason"`
}

func (s *Server) graph(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if _, err := s.store.GetRun(r.Context(), runID); err != nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	facts, err := s.store.ListFacts(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	intents, err := s.store.ListIntents(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	hints, err := s.store.ListHints(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	attempts, err := s.store.ListIntentAttempts(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	occupied := map[int]bool{}
	for _, intent := range intents {
		if intent.State == harness.IntentClaimed {
			occupied[intent.Slot] = true
		}
	}
	slots := make([]int, 0, 2)
	for _, slot := range []int{1, 2} {
		if !occupied[slot] {
			slots = append(slots, slot)
		}
	}
	revision, _ := s.store.GetBoardRevision(r.Context(), runID)
	reason := graphReason{FreeSlots: slots, FrontierVersion: revision}
	events, _ := s.store.ListEvents(r.Context(), runID, 0)
	for _, event := range events {
		switch event.Type {
		case harness.EventReasonStarted:
			reason.Active = true
			var payload harness.ReasonPayload
			if json.Unmarshal(event.Data, &payload) == nil {
				reason.FrontierVersion = payload.FrontierVersion
			}
		case harness.EventReasonCompleted, harness.EventReasonRejected:
			reason.Active = false
		}
	}
	writeJSON(w, http.StatusOK, graphResponse{Facts: facts, Intents: intents, Hints: hints, Attempts: attempts, Slots: slots, BoardRevision: revision, Reason: reason})
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
		// The Run may have finalized between the initial replay query and the
		// live-handle lookup. Re-read the tail so run.finalized cannot be lost.
		remaining, err := s.store.ListEvents(r.Context(), runID, after)
		if err != nil {
			return
		}
		writeEvents(remaining)
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
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	tail := r.URL.Query().Get("tail") == "true"
	items, err := s.store.ListLogs(r.Context(), runID, after)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// tail=limit returns the newest page, which is what a client needs to open
	// an existing log stream. limit alone keeps the oldest page after_seq.
	if tail && limit > 0 && len(items) > limit {
		items = items[len(items)-limit:]
	} else if !tail && limit > 0 && len(items) > limit {
		items = items[:limit]
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

func (s *Server) sweepListCreate(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.createSweep(w, r)
	case http.MethodGet:
		s.mu.Lock()
		out := make([]SweepState, 0, len(s.sweeps))
		for _, state := range s.sweeps {
			out = append(out, snapshotSweepLocked(state))
		}
		s.mu.Unlock()
		if len(out) == 0 {
			if reader, ok := s.store.(SweepStore); ok {
				records, err := reader.ListSweeps(r.Context(), 100)
				if err != nil {
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
				for _, record := range records {
					var state SweepState
					if json.Unmarshal(record.Result, &state) == nil {
						out = append(out, state)
					}
				}
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
		sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
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
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid sweep request: "+err.Error())
		return
	}
	if err := harness.ValidateDescriptor(request.Descriptor); err != nil {
		writeError(w, http.StatusBadRequest, "invalid sweep descriptor: "+err.Error())
		return
	}
	plan, err := s.sweepBuilder(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(plan.ChallengeCodes) == 0 || plan.Build == nil {
		writeError(w, http.StatusBadRequest, "sweep plan requires challenge codes and a builder")
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
	for _, rawCode := range plan.ChallengeCodes {
		code := rawCode
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			spec, err := plan.Build(code)
			if err != nil {
				s.appendSweepError(state, fmt.Errorf("build %s: %w", code, err))
				return
			}
			run, err := s.runner.Start(context.WithoutCancel(context.Background()), spec)
			if err != nil {
				s.appendSweepError(state, fmt.Errorf("start %s: %w", code, err))
				return
			}
			s.mu.Lock()
			state.RunIDs = append(state.RunIDs, run.ID)
			s.mu.Unlock()
			result, runErr := run.Wait(context.Background())
			s.mu.Lock()
			state.Results = append(state.Results, result)
			s.mu.Unlock()
			if runErr != nil {
				s.appendSweepError(state, fmt.Errorf("wait %s: %w", code, runErr))
			}
		}()
	}
	wg.Wait()
	s.updateSweep(state, func() {
		finished := time.Now().UTC()
		state.FinishedAt = &finished
		state.Status = harness.StatusSucceeded
		if state.Error != "" || len(state.Results) != len(plan.ChallengeCodes) {
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

func (s *Server) persistSweep(snapshot SweepState) {
	writer, ok := s.store.(SweepStore)
	if !ok {
		return
	}
	result, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	_ = writer.SaveSweep(context.Background(), harness.SweepRecord{
		ID: snapshot.ID, Status: snapshot.Status, Result: result,
		Error: snapshot.Error, CreatedAt: snapshot.CreatedAt, FinishedAt: snapshot.FinishedAt,
	})
}

func (s *Server) appendSweepError(state *SweepState, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if state.Error != "" {
		state.Error += "; "
	}
	state.Error += err.Error()
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
		reader, ok := s.store.(SweepStore)
		if !ok {
			writeError(w, http.StatusNotFound, "sweep not found")
			return
		}
		record, err := reader.GetSweep(r.Context(), id)
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
