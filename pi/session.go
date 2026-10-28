package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/red-copilot/harness/internal/naming"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/red-copilot/harness"
)

// DefaultImage is the pinned image the SDK ships for Pi in Kali.
const DefaultImage = "ghcr.io/red-copilot/harness-pi-kali:0.1.0"

// Config is immutable Pi adapter configuration. RuntimeConfig on an AgentSpec
// may override these fields with the same JSON shape.
type Config struct {
	Provider     string   `json:"provider,omitempty" yaml:"provider,omitempty"`
	Model        string   `json:"model,omitempty" yaml:"model,omitempty"`
	Thinking     string   `json:"thinking,omitempty" yaml:"thinking,omitempty"`
	SystemPrompt string   `json:"system_prompt,omitempty" yaml:"system_prompt,omitempty"`
	ModelsJSON   string   `json:"models_json,omitempty" yaml:"models_json,omitempty"`
	Tools        []string `json:"tools,omitempty" yaml:"tools,omitempty"`
	ExtraArgs    []string `json:"extra_args,omitempty" yaml:"extra_args,omitempty"`
	Skills       []string `json:"skills,omitempty" yaml:"skills,omitempty"`
	Image        string   `json:"image,omitempty" yaml:"image,omitempty"`
}

// Factory creates Pi RPC sessions from runtime-neutral requests.
type Factory struct{ config Config }

func NewFactory(config ...Config) *Factory {
	factory := &Factory{}
	if len(config) > 0 {
		factory.config = config[0]
	}
	return factory
}

func (f *Factory) NewSession(ctx context.Context, req harness.RuntimeRequest) (harness.AgentSession, error) {
	if req.Starter == nil {
		return nil, errors.New("pi session requires a ProcessStarter")
	}
	config := f.config
	if len(req.Agent.RuntimeConfig) > 0 {
		var override Config
		if err := json.Unmarshal(req.Agent.RuntimeConfig, &override); err != nil {
			return nil, fmt.Errorf("invalid pi runtime config: %w", err)
		}
		config = mergeConfig(config, override)
	}
	if config.ModelsJSON != "" {
		if !json.Valid([]byte(config.ModelsJSON)) {
			return nil, errors.New("pi models_json is not valid JSON")
		}
		piDir := filepath.Join(req.WorkspaceDir, ".pi")
		if err := os.MkdirAll(piDir, 0o700); err != nil {
			return nil, fmt.Errorf("create pi config directory: %w", err)
		}
		// models.json frequently carries provider keys. It lands in a path the
		// agent can read, so the Run's known secrets are removed before the file
		// is written. Credentials belong in the agent environment, not here.
		modelsJSON := config.ModelsJSON
		if req.Redactor != nil {
			modelsJSON = req.Redactor.Redact(modelsJSON)
		}
		if err := os.WriteFile(filepath.Join(piDir, "models.json"), []byte(modelsJSON), 0o600); err != nil {
			return nil, fmt.Errorf("write pi models.json: %w", err)
		}
	}
	skills, err := discoverSkills(req.SkillsDir)
	if err != nil {
		return nil, fmt.Errorf("discover skills: %w", err)
	}
	command := BuildCommand(req.Agent, req.Role, config, skills...)
	image := req.Agent.Image
	if image == "" {
		image = config.Image
	}
	if image == "" {
		image = DefaultImage
	}
	network := req.Agent.Network
	if network == "" {
		network = "bridge"
	}
	if req.Target.Runtime != nil {
		if value := req.Target.Runtime.Network; value != "" {
			network = value
		}
	}
	caps := append([]string(nil), req.Agent.CapAdd...)
	if len(caps) == 0 && req.Role == harness.AgentRoleExecutor {
		caps = []string{"NET_RAW"}
	}
	env := make(map[string]string, len(req.Env)+4)
	for key, value := range req.Env {
		env[key] = value
	}
	env["PI_CODING_AGENT_DIR"] = "/workspace/.pi"
	env["HOME"] = "/workspace"
	mounts := []harness.Mount{{Source: req.TaskDir, Destination: "/task", ReadOnly: true}, {Source: req.SkillsDir, Destination: "/skills", ReadOnly: true}, {Source: req.WorkspaceDir, Destination: "/workspace", ReadOnly: false}}
	mounts = append(mounts, req.Agent.Mounts...)
	resources := req.Agent.Resources
	if resources.PidsLimit == 0 {
		resources.PidsLimit = 256
	}
	if resources.Memory == "" {
		resources.Memory = "2g"
	}
	if resources.CPUs == "" {
		resources.CPUs = "2"
	}
	spec := harness.ProcessSpec{Image: image, Command: command, Env: env, Mounts: mounts, Network: network, CapAdd: caps, Name: "harness_pi_" + naming.Identifier(req.RunID+"_"+req.SessionID, "session"), Labels: map[string]string{"harness.run_id": req.RunID, "harness.session_id": req.SessionID}, WorkingDir: "/workspace", User: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), ReadOnlyRoot: true, PidsLimit: resources.PidsLimit, Memory: resources.Memory, CPUs: resources.CPUs}
	proc, err := req.Starter.Start(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("start pi process: %w", err)
	}
	return newSession(proc), nil
}

func mergeConfig(base, override Config) Config {
	if override.Provider != "" {
		base.Provider = override.Provider
	}
	if override.Model != "" {
		base.Model = override.Model
	}
	if override.Thinking != "" {
		base.Thinking = override.Thinking
	}
	if override.SystemPrompt != "" {
		base.SystemPrompt = override.SystemPrompt
	}
	if override.ModelsJSON != "" {
		base.ModelsJSON = override.ModelsJSON
	}
	if len(override.Tools) > 0 {
		base.Tools = append([]string(nil), override.Tools...)
	}
	if len(override.ExtraArgs) > 0 {
		base.ExtraArgs = append([]string(nil), override.ExtraArgs...)
	}
	if len(override.Skills) > 0 {
		base.Skills = append([]string(nil), override.Skills...)
	}
	if override.Image != "" {
		base.Image = override.Image
	}
	return base
}

// BuildCommand returns the fixed, hardened Pi RPC command line.
func BuildCommand(agent harness.AgentSpec, role harness.AgentRole, config Config, skills ...string) []string {
	args := []string{"pi", "--mode", "rpc", "--no-session", "--no-approve", "--no-context-files", "--no-extensions", "--no-mcp", "--no-prompt-templates", "--no-themes", "--no-skills"}
	for _, skill := range skills {
		args = append(args, "--skill", skill)
	}
	for _, skill := range config.Skills {
		args = append(args, "--skill", skill)
	}
	tools := append([]string(nil), config.Tools...)
	if len(tools) == 0 {
		if role == harness.AgentRoleReasoner {
			tools = []string{"read"}
		} else {
			tools = []string{"read", "bash", "edit", "write"}
		}
	}
	args = append(args, "--tools", strings.Join(tools, ","), "--offline")
	if config.Provider != "" {
		args = append(args, "--provider", config.Provider)
	}
	if config.Model != "" {
		args = append(args, "--model", config.Model)
	}
	if config.Thinking != "" {
		args = append(args, "--thinking", config.Thinking)
	}
	if config.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", config.SystemPrompt)
	}
	return append(args, config.ExtraArgs...)
}

func discoverSkills(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var skills []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := os.Stat(filepath.Join(root, entry.Name(), "SKILL.md"))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if !info.IsDir() {
			skills = append(skills, filepath.ToSlash(filepath.Join("/skills", entry.Name())))
		}
	}
	sort.Strings(skills)
	return skills, nil
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

	pendingMu sync.Mutex
	pending   map[string]chan responseRecord
	nextID    atomic.Uint64

	writeMu   sync.Mutex
	stdinOnce sync.Once

	streamMu      sync.RWMutex
	streamsClosed bool

	usageMu sync.Mutex
	usage   harness.Usage

	exited   chan struct{}
	exitOnce sync.Once

	ctx       context.Context
	cancel    context.CancelFunc
	readWG    sync.WaitGroup
	closeOnce sync.Once
	closed    chan struct{}
	closing   atomic.Bool
}

func newSession(proc harness.Process) *session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{
		proc: proc, stdin: proc.Stdin(), stdout: bufio.NewReader(proc.Stdout()),
		stderr: bufio.NewReader(proc.Stderr()), events: make(chan harness.AgentEvent, 256),
		logs:    make(chan harness.LogEvent, 256),
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
	_ = data
	return nil
}

func (s *session) Steer(ctx context.Context, message string) error {
	_, err := s.sendAndWait(ctx, map[string]any{"type": "steer", "message": message})
	return err
}

// Reset discards Pi's conversation history while keeping the process and
// transport available for the next frontier.
func (s *session) Reset(ctx context.Context) error {
	_, err := s.sendAndWait(ctx, map[string]any{"type": "new_session"})
	return err
}

func (s *session) Events() <-chan harness.AgentEvent { return s.events }
func (s *session) Logs() <-chan harness.LogEvent     { return s.logs }
func (s *session) Usage() harness.Usage              { return s.snapshotUsage() }

func (s *session) Abort(ctx context.Context) error {
	_, err := s.sendAndWait(ctx, map[string]any{"type": "abort"})
	return err
}

func (s *session) Close(ctx context.Context) error {
	var err error
	s.closeOnce.Do(func() {
		s.closing.Store(true)
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
			if line[len(line)-1] != '\n' {
				s.emitEvent(harness.AgentEvent{Type: "rpc.malformed", Data: mustRaw(map[string]string{"error": "strict LF framing required", "line": string(line)})})
				return
			}
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
	s.emitEvent(normalizeEvent(recordType, line))
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
	if !s.closing.Load() {
		message := "pi process exited before settlement"
		if err != nil {
			message += ": " + err.Error()
		} else if status.Code != 0 {
			message += fmt.Sprintf(" with exit code %d", status.Code)
		}
		s.emitEvent(harness.AgentEvent{
			Type: "rpc.exit", Kind: harness.AgentEventError, Text: message,
			Data: mustRaw(map[string]any{"exit_code": status.Code, "error": errorString(err)}),
		})
	}
	s.exitOnce.Do(func() { close(s.exited) })
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
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

func (s *session) snapshotUsage() harness.Usage {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	return s.usage
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

// normalizeEvent translates one Pi RPC record into the runtime-neutral signal
// the control plane consumes. The native type and payload are kept verbatim
// for the audit trail; everything the core acts on is resolved here.
func normalizeEvent(recordType string, line []byte) harness.AgentEvent {
	event := harness.AgentEvent{Type: recordType, Data: append([]byte(nil), line...)}
	switch recordType {
	case "turn_end", "message_end":
		event.Kind = harness.AgentEventMessage
		event.Text, event.Tools = parseTurnMessage(line)
	case "tool_execution_start":
		event.Kind = harness.AgentEventToolStart
		event.Tool, event.ToolPath, event.WritesFile = parseToolStart(line)
	case "tool_execution_end":
		event.Kind = harness.AgentEventToolEnd
	case "agent_settled":
		event.Kind = harness.AgentEventSettled
	case "agent_end":
		event.Kind = harness.AgentEventEnd
	}
	return event
}

func parseTurnMessage(line []byte) (string, []string) {
	var payload struct {
		Message struct {
			Content []struct{ Type, Text, Name string } `json:"content"`
		} `json:"message"`
		ToolResults []struct {
			ToolName string `json:"toolName"`
		} `json:"toolResults"`
	}
	if json.Unmarshal(line, &payload) != nil {
		return "", nil
	}
	var text []string
	var tools []string
	for _, part := range payload.Message.Content {
		switch part.Type {
		case "text":
			if strings.TrimSpace(part.Text) != "" {
				text = append(text, part.Text)
			}
		case "toolCall":
			if part.Name != "" {
				tools = append(tools, part.Name)
			}
		}
	}
	if len(tools) == 0 {
		for _, tool := range payload.ToolResults {
			if tool.ToolName != "" {
				tools = append(tools, tool.ToolName)
			}
		}
	}
	return strings.Join(text, "\n"), tools
}

// parseToolStart extracts the tool name and, for Pi's write tool, the target
// path. WritesFile marks the protocol-mandated report write for the core.
func parseToolStart(line []byte) (tool, path string, writesFile bool) {
	var payload struct {
		ToolName string `json:"toolName"`
		Args     struct {
			Path string `json:"path"`
		} `json:"args"`
	}
	if json.Unmarshal(line, &payload) != nil {
		return "", "", false
	}
	return payload.ToolName, payload.Args.Path, payload.ToolName == "write"
}

func mustRaw(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

var _ harness.AgentSession = (*session)(nil)
