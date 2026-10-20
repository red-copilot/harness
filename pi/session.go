package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/red-copilot/harness"
)

// DefaultImage is the pinned image the SDK ships for Pi in Kali.
const DefaultImage = "ghcr.io/red-copilot/harness-pi-kali:0.1.0"

const tsecSkillPath = "/skills/tsec-submit"

// Factory creates Pi RPC sessions from harness.SessionRequest values.
type Factory struct{}

func NewFactory() *Factory { return &Factory{} }

func (f *Factory) NewSession(ctx context.Context, req harness.SessionRequest) (harness.AgentSession, error) {
	if req.Starter == nil {
		return nil, errors.New("pi session requires a ProcessStarter")
	}
	piSpec := harness.PiSpec{}
	if req.Agent.Pi != nil {
		piSpec = *req.Agent.Pi
	}
	image := req.Agent.Image
	if image == "" {
		image = piSpec.Image
	}
	if image == "" {
		image = DefaultImage
	}
	if piSpec.ModelsJSON != "" {
		if !json.Valid([]byte(piSpec.ModelsJSON)) {
			return nil, errors.New("pi models_json is not valid JSON")
		}
		piDir := filepath.Join(req.WorkspaceDir, ".pi")
		if err := os.MkdirAll(piDir, 0o700); err != nil {
			return nil, fmt.Errorf("create pi config directory: %w", err)
		}
		if err := os.WriteFile(filepath.Join(piDir, "models.json"), []byte(piSpec.ModelsJSON), 0o600); err != nil {
			return nil, fmt.Errorf("write pi models.json: %w", err)
		}
	}
	command := BuildCommand(req.Agent, tsecSkillPath)
	network := req.Agent.Network
	if network == "" {
		network = "bridge"
	}
	if value := req.Target.Metadata["network"]; value != "" {
		network = value
	}
	caps := req.Agent.CapAdd
	if caps == nil {
		caps = []string{"NET_RAW"}
	}
	env := make(map[string]string, len(req.Env)+4)
	for key, value := range req.Env {
		env[key] = value
	}
	env["PI_CODING_AGENT_DIR"] = "/workspace/.pi"
	env["HOME"] = "/workspace"
	spec := harness.ProcessSpec{
		Image: image, Command: command, Env: env,
		Mounts: []harness.Mount{
			{Source: req.TaskDir, Destination: "/task", ReadOnly: true},
			{Source: req.SkillsDir, Destination: "/skills", ReadOnly: true},
			{Source: req.WorkspaceDir, Destination: "/workspace", ReadOnly: false},
		},
		Network: network, CapAdd: caps,
		Name:       "harness_pi_" + safeName(req.RunID+"_"+req.SessionID),
		Labels:     map[string]string{"harness.run_id": req.RunID, "harness.session_id": req.SessionID},
		WorkingDir: "/workspace", User: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), ReadOnlyRoot: true,
		PidsLimit: 256, Memory: "2g", CPUs: "2",
	}
	proc, err := req.Starter.Start(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("start pi process: %w", err)
	}
	return newSession(proc), nil
}

// BuildCommand returns the fixed, hardened Pi RPC command line.
func BuildCommand(agent harness.AgentSpec, tsecSkill string) []string {
	piSpec := harness.PiSpec{}
	if agent.Pi != nil {
		piSpec = *agent.Pi
	}
	args := []string{
		"pi", "--mode", "rpc", "--no-session", "--no-approve",
		"--no-context-files", "--no-extensions", "--no-mcp",
		"--no-prompt-templates", "--no-themes", "--no-skills",
		"--skill", tsecSkill,
	}
	for _, skill := range piSpec.Skills {
		args = append(args, "--skill", skill)
	}
	tools := piSpec.Tools
	if len(tools) == 0 {
		tools = []string{"read", "bash", "edit", "write"}
	}
	args = append(args, "--tools", strings.Join(tools, ","), "--offline")
	if piSpec.Provider != "" {
		args = append(args, "--provider", piSpec.Provider)
	}
	if piSpec.Model != "" {
		args = append(args, "--model", piSpec.Model)
	}
	if piSpec.Thinking != "" {
		args = append(args, "--thinking", piSpec.Thinking)
	}
	if piSpec.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", piSpec.SystemPrompt)
	}
	return append(args, piSpec.ExtraArgs...)
}

type responseRecord struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

type session struct {
	proc   harness.Process
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *bufio.Reader

	events chan harness.AgentEvent
	logs   chan harness.LogEvent

	settledMu   sync.Mutex
	settled     chan struct{}
	settledOnce sync.Once

	pendingMu sync.Mutex
	pending   map[string]chan responseRecord
	nextID    atomic.Uint64

	writeMu   sync.Mutex
	stdinOnce sync.Once

	streamMu      sync.RWMutex
	streamsClosed bool

	usageMu sync.Mutex
	usage   harness.Usage

	resultMu sync.Mutex
	result   harness.AgentResult

	exited   chan struct{}
	exitOnce sync.Once

	ctx       context.Context
	cancel    context.CancelFunc
	readWG    sync.WaitGroup
	closeOnce sync.Once
	closed    chan struct{}
}

func newSession(proc harness.Process) *session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{
		proc: proc, stdin: proc.Stdin(), stdout: bufio.NewReader(proc.Stdout()),
		stderr: bufio.NewReader(proc.Stderr()), events: make(chan harness.AgentEvent, 256),
		logs: make(chan harness.LogEvent, 256), settled: make(chan struct{}),
		pending: map[string]chan responseRecord{}, exited: make(chan struct{}),
		ctx: ctx, cancel: cancel, closed: make(chan struct{}),
	}
	s.readWG.Add(2)
	go func() { defer s.readWG.Done(); s.readStdout() }()
	go func() { defer s.readWG.Done(); s.readStderr() }()
	go s.waitProcess()
	return s
}

func (s *session) Prompt(ctx context.Context, message string) error {
	record, err := s.sendAndWait(ctx, map[string]any{"type": "prompt", "message": message})
	if err != nil {
		return err
	}
	var data struct {
		Disposition string `json:"disposition"`
	}
	_ = json.Unmarshal(record.Data, &data)
	if data.Disposition == "handled" {
		s.markSettled(harness.StatusSucceeded, "")
	}
	return nil
}

func (s *session) Steer(ctx context.Context, message string) error {
	_, err := s.sendAndWait(ctx, map[string]any{"type": "steer", "message": message})
	return err
}

func (s *session) Events() <-chan harness.AgentEvent { return s.events }
func (s *session) Logs() <-chan harness.LogEvent     { return s.logs }
func (s *session) Usage() harness.Usage              { return s.snapshotUsage() }

func (s *session) Wait(ctx context.Context) (harness.AgentResult, error) {
	select {
	case <-s.settled:
		return s.snapshotResult(), nil
	case <-s.exited:
		return s.snapshotResult(), nil
	case <-ctx.Done():
		return harness.AgentResult{
			Status: harness.StatusCancelled, Error: ctx.Err().Error(), Usage: s.snapshotUsage(),
		}, ctx.Err()
	}
}

func (s *session) Abort(ctx context.Context) error {
	_, err := s.sendAndWait(ctx, map[string]any{"type": "abort"})
	return err
}

func (s *session) Close(ctx context.Context) error {
	var err error
	s.closeOnce.Do(func() {
		s.closeStdin()
		select {
		case <-s.exited:
		case <-time.After(2 * time.Second):
			_ = s.proc.Kill(ctx)
		case <-ctx.Done():
			_ = s.proc.Kill(ctx)
		}
		s.cancel()
		_ = s.proc.Kill(ctx)
		s.waitReaders(2 * time.Second)
		s.streamMu.Lock()
		s.streamsClosed = true
		close(s.events)
		close(s.logs)
		s.streamMu.Unlock()
		close(s.closed)
	})
	return err
}

func (s *session) sendAndWait(ctx context.Context, command map[string]any) (responseRecord, error) {
	id := fmt.Sprintf("harness-%d", s.nextID.Add(1))
	command["id"] = id
	response := make(chan responseRecord, 1)
	s.pendingMu.Lock()
	s.pending[id] = response
	s.pendingMu.Unlock()
	defer func() {
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
	}()
	data, err := json.Marshal(command)
	if err != nil {
		return responseRecord{}, err
	}
	if err := s.writeCommand(ctx, append(data, '\n')); err != nil {
		return responseRecord{}, err
	}
	select {
	case record := <-response:
		if !record.Success {
			if record.Error == "" {
				record.Error = "pi command failed"
			}
			return record, errors.New(record.Error)
		}
		return record, nil
	case <-ctx.Done():
		return responseRecord{}, ctx.Err()
	case <-s.exited:
		return responseRecord{}, errors.New("pi exited before responding")
	}
}

func (s *session) writeCommand(ctx context.Context, data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, err := s.stdin.Write(data)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("write rpc command: %w", err)
		}
		return nil
	case <-ctx.Done():
		s.closeStdin()
		return ctx.Err()
	case <-s.exited:
		s.closeStdin()
		return errors.New("pi exited before writing command")
	}
}

func (s *session) closeStdin() {
	s.stdinOnce.Do(func() {
		if s.stdin != nil {
			_ = s.stdin.Close()
		}
	})
}

func (s *session) waitReaders(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		s.readWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

func (s *session) readStdout() {
	for {
		line, err := s.stdout.ReadBytes('\n')
		if len(line) > 0 {
			s.handleStdoutLine(line)
		}
		if err != nil {
			return
		}
	}
}

func (s *session) handleStdoutLine(line []byte) {
	line = trimRecord(line)
	if len(line) == 0 {
		return
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(line, &record); err != nil {
		s.emitEvent(harness.AgentEvent{Type: "rpc.malformed", Data: mustRaw(map[string]string{"error": err.Error(), "line": string(line)})})
		return
	}
	recordType := rawString(record["type"])
	if recordType == "response" {
		id := rawString(record["id"])
		if id != "" {
			var response responseRecord
			_ = json.Unmarshal(line, &response)
			s.pendingMu.Lock()
			pending := s.pending[id]
			s.pendingMu.Unlock()
			if pending != nil {
				pending <- response
				return
			}
		}
	}
	s.account(recordType, record)
	if recordType == "agent_settled" {
		var payload struct {
			Aborted bool `json:"aborted"`
		}
		_ = json.Unmarshal(line, &payload)
		status := harness.StatusSucceeded
		if payload.Aborted {
			status = harness.StatusCancelled
		}
		s.markSettled(status, "")
	}
	s.emitEvent(harness.AgentEvent{Type: recordType, Data: append([]byte(nil), line...)})
}

func (s *session) readStderr() {
	for {
		line, err := s.stderr.ReadString('\n')
		if len(line) > 0 {
			s.streamMu.RLock()
			if s.streamsClosed {
				s.streamMu.RUnlock()
				return
			}
			select {
			case s.logs <- harness.LogEvent{Stream: "stderr", Text: line}:
			case <-s.ctx.Done():
			}
			s.streamMu.RUnlock()
		}
		if err != nil {
			return
		}
	}
}

func (s *session) waitProcess() {
	status, err := s.proc.Wait(s.ctx)
	s.resultMu.Lock()
	if s.result.Status == "" {
		s.result.Status = harness.StatusFailed
		if err != nil {
			s.result.Error = err.Error()
		} else if status.Code != 0 {
			s.result.Error = "pi exited with code " + strconv.Itoa(status.Code)
		} else {
			s.result.Error = "pi exited before agent_settled"
		}
	}
	s.result.ExitCode = status.Code
	s.resultMu.Unlock()
	s.exitOnce.Do(func() { close(s.exited) })
}

func (s *session) account(recordType string, record map[string]json.RawMessage) {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	switch recordType {
	case "message_start":
		var message struct {
			Role string `json:"role"`
		}
		_ = json.Unmarshal(record["message"], &message)
		if message.Role == "assistant" {
			s.usage.ModelCalls++
		}
	case "message_end":
		var message struct {
			Role  string          `json:"role"`
			Usage json.RawMessage `json:"usage"`
		}
		_ = json.Unmarshal(record["message"], &message)
		if message.Role == "assistant" && len(message.Usage) > 0 {
			addUsage(&s.usage, message.Usage)
		}
	case "tool_execution_start":
		s.usage.ToolCalls++
	}
	if usageJSON := record["usage"]; len(usageJSON) > 0 {
		addUsage(&s.usage, usageJSON)
	}
}

func (s *session) markSettled(status harness.Status, message string) {
	s.settledOnce.Do(func() {
		s.resultMu.Lock()
		s.result.Status = status
		s.result.Error = message
		s.result.Usage = s.snapshotUsage()
		s.resultMu.Unlock()
		close(s.settled)
	})
}

func (s *session) snapshotUsage() harness.Usage {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	return s.usage
}

func (s *session) snapshotResult() harness.AgentResult {
	s.resultMu.Lock()
	defer s.resultMu.Unlock()
	result := s.result
	result.Usage = s.snapshotUsage()
	return result
}

func (s *session) emitEvent(event harness.AgentEvent) {
	s.streamMu.RLock()
	defer s.streamMu.RUnlock()
	if s.streamsClosed {
		return
	}
	select {
	case s.events <- event:
	case <-s.ctx.Done():
	}
}

func addUsage(usage *harness.Usage, raw json.RawMessage) {
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return
	}
	usage.InputTokens += asInt(value["input"])
	usage.OutputTokens += asInt(value["output"])
	usage.TotalTokens += asInt(value["totalTokens"])
	if usage.TotalTokens == 0 {
		usage.TotalTokens += asInt(value["total_tokens"])
	}
	if cost, ok := value["cost"].(map[string]any); ok {
		usage.CostUSD += asFloat(cost["total"])
	}
}

func asInt(v any) int {
	switch value := v.(type) {
	case float64:
		return int(value)
	case json.Number:
		n, _ := value.Int64()
		return int(n)
	case int:
		return value
	default:
		return 0
	}
}

func asFloat(v any) float64 {
	switch value := v.(type) {
	case float64:
		return value
	case json.Number:
		f, _ := value.Float64()
		return f
	default:
		return 0
	}
}

func trimRecord(line []byte) []byte {
	line = bytesTrimSuffix(line, []byte("\n"))
	line = bytesTrimSuffix(line, []byte("\r"))
	return line
}

func bytesTrimSuffix(value, suffix []byte) []byte {
	if len(value) >= len(suffix) && string(value[len(value)-len(suffix):]) == string(suffix) {
		return value[:len(value)-len(suffix)]
	}
	return value
}

func rawString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func safeName(value string) string {
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "session"
	}
	return b.String()
}

func mustRaw(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

var _ harness.AgentSession = (*session)(nil)
var _ harness.LogSource = (*session)(nil)
var _ harness.UsageSource = (*session)(nil)
