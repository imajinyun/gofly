package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultRPCMuxSubprocessTimeout        = time.Second
	defaultRPCMuxSubprocessMaxArgs        = 16
	defaultRPCMuxSubprocessMaxOutputBytes = 64 << 10

	RPCMuxSubprocessPolicyErrorCommandDenied     = "command_denied"
	RPCMuxSubprocessPolicyErrorWorkDirEscaped    = "workdir_escaped"
	RPCMuxSubprocessPolicyErrorEnvNotWhitelisted = "env_not_whitelisted"
)

// RPCMuxSubprocessExporterConfig configures a local subprocess exporter. The
// executable and arguments are passed directly to exec.CommandContext; shell
// command strings are intentionally unsupported.
type RPCMuxSubprocessExporterConfig struct {
	Command        string            `json:"command"`
	Args           []string          `json:"args,omitempty"`
	Timeout        time.Duration     `json:"timeout,omitempty"`
	MaxOutputBytes int64             `json:"maxOutputBytes,omitempty"`
	WorkDir        string            `json:"workDir,omitempty"`
	WorkDirRoot    string            `json:"workDirRoot,omitempty"`
	AllowCommands  []string          `json:"allowCommands,omitempty"`
	DenyCommands   []string          `json:"denyCommands,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	EnvWhitelist   []string          `json:"envWhitelist,omitempty"`
}

// RPCMuxSubprocessExecutionPolicy is the reusable validation contract for local
// subprocess execution. It is shared by profile validation and runtime
// construction so security checks cannot drift.
type RPCMuxSubprocessExecutionPolicy struct {
	Command       string
	Args          []string
	WorkDir       string
	WorkDirRoot   string
	AllowCommands []string
	DenyCommands  []string
	Env           map[string]string
	EnvWhitelist  []string
}

type RPCMuxSubprocessExecutionPolicyError struct {
	Category string
	Message  string
}

func (e RPCMuxSubprocessExecutionPolicyError) Error() string {
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = "subprocess exporter policy validation failed"
	}
	category := strings.TrimSpace(e.Category)
	if category == "" {
		return message
	}
	return fmt.Sprintf("[%s] %s", category, message)
}

func (c RPCMuxSubprocessExporterConfig) ExecutionPolicy() RPCMuxSubprocessExecutionPolicy {
	return RPCMuxSubprocessExecutionPolicy{
		Command:       c.Command,
		Args:          c.Args,
		WorkDir:       c.WorkDir,
		WorkDirRoot:   c.WorkDirRoot,
		AllowCommands: c.AllowCommands,
		DenyCommands:  c.DenyCommands,
		Env:           c.Env,
		EnvWhitelist:  c.EnvWhitelist,
	}
}

// RPCMuxSubprocessExporterSnapshot exposes subprocess delivery diagnostics
// without event payloads, environment values, or profile material.
type RPCMuxSubprocessExporterSnapshot struct {
	Command             string                                  `json:"command"`
	ArgsCount           int                                     `json:"argsCount"`
	WorkDir             string                                  `json:"workDir,omitempty"`
	Timeout             time.Duration                           `json:"timeout"`
	MaxOutputBytes      int64                                   `json:"maxOutputBytes"`
	Policy              RPCMuxSubprocessExecutionPolicySnapshot `json:"policy"`
	Runs                int64                                   `json:"runs"`
	LastExitCode        int                                     `json:"lastExitCode,omitempty"`
	LastDuration        time.Duration                           `json:"lastDuration,omitempty"`
	LastTimedOut        bool                                    `json:"lastTimedOut,omitempty"`
	LastOutputTruncated bool                                    `json:"lastOutputTruncated,omitempty"`
	LastError           string                                  `json:"lastError,omitempty"`
	LastRunAt           time.Time                               `json:"lastRunAt,omitempty"`
}

// RPCMuxSubprocessExecutionPolicySnapshot is a redacted summary of the local
// process execution policy. It intentionally omits env values.
type RPCMuxSubprocessExecutionPolicySnapshot struct {
	AllowCommands []string `json:"allowCommands,omitempty"`
	DenyCommands  []string `json:"denyCommands,omitempty"`
	WorkDirRoot   string   `json:"workDirRoot,omitempty"`
	EnvWhitelist  []string `json:"envWhitelist,omitempty"`
	EnvCount      int      `json:"envCount,omitempty"`
}

type rpcMuxSubprocessExporter struct {
	config   RPCMuxSubprocessExporterConfig
	mu       sync.RWMutex
	snapshot RPCMuxSubprocessExporterSnapshot
}

// RPCMuxSubprocessExporterSnapshotter exposes subprocess delivery diagnostics.
type RPCMuxSubprocessExporterSnapshotter interface {
	RPCMuxSubprocessExporterSnapshot() RPCMuxSubprocessExporterSnapshot
}

type rpcMuxSubprocessOTelLogSinkProvider struct{}

// NewRPCMuxSubprocessDiagnosisEventExporter creates a local subprocess exporter
// that writes one JSON event envelope to stdin per export call.
func NewRPCMuxSubprocessDiagnosisEventExporter(config RPCMuxSubprocessExporterConfig) (RPCMuxDiagnosisEventExporter, error) {
	config, err := normalizeRPCMuxSubprocessExporterConfig(config)
	if err != nil {
		return nil, err
	}
	return &rpcMuxSubprocessExporter{
		config: config,
		snapshot: RPCMuxSubprocessExporterSnapshot{
			Command:        config.Command,
			ArgsCount:      len(config.Args),
			WorkDir:        config.WorkDir,
			Timeout:        config.Timeout,
			MaxOutputBytes: config.MaxOutputBytes,
			Policy:         subprocessExecutionPolicySnapshot(config),
			LastExitCode:   -1,
		},
	}, nil
}

func (rpcMuxSubprocessOTelLogSinkProvider) ValidateRPCMuxOTelLogProfile(profile string) error {
	var config RPCMuxSubprocessExporterConfig
	if err := DecodeRPCMuxOTelLogProfile(profile, &config); err != nil {
		return err
	}
	_, err := normalizeRPCMuxSubprocessExporterConfig(config)
	return err
}

func (rpcMuxSubprocessOTelLogSinkProvider) NewRPCMuxOTelLogExporter(profile string) RPCMuxOTelLogExporter {
	var config RPCMuxSubprocessExporterConfig
	if err := DecodeRPCMuxOTelLogProfile(profile, &config); err != nil {
		return nil
	}
	exporter, err := NewRPCMuxSubprocessDiagnosisEventExporter(config)
	if err != nil {
		return nil
	}
	return rpcMuxSubprocessOTelLogExporter{exporter: exporter}
}

func (rpcMuxSubprocessOTelLogSinkProvider) RPCMuxOTelLogProfileSchema() json.RawMessage {
	return json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false,"required":["command"],"x-gofly-policy-error-categories":["command_denied","workdir_escaped","env_not_whitelisted"],"properties":{"command":{"type":"string","minLength":1},"args":{"type":"array","maxItems":16,"items":{"type":"string"}},"timeout":{"type":"integer","minimum":1},"maxOutputBytes":{"type":"integer","minimum":1},"workDir":{"type":"string"},"workDirRoot":{"type":"string"},"allowCommands":{"type":"array","items":{"type":"string"}},"denyCommands":{"type":"array","items":{"type":"string"}},"env":{"type":"object","additionalProperties":{"type":"string"}},"envWhitelist":{"type":"array","items":{"type":"string"}}}}`)
}

func (rpcMuxSubprocessOTelLogSinkProvider) RPCMuxOTelLogValidationCategories() []string {
	return []string{
		RPCMuxSubprocessPolicyErrorCommandDenied,
		RPCMuxSubprocessPolicyErrorWorkDirEscaped,
		RPCMuxSubprocessPolicyErrorEnvNotWhitelisted,
	}
}

type rpcMuxSubprocessOTelLogExporter struct {
	exporter RPCMuxDiagnosisEventExporter
}

func (e rpcMuxSubprocessOTelLogExporter) ExportRPCMuxOTelLog(ctx context.Context, record RPCMuxDiagnosisEventOTelLogRecord) {
	if e.exporter == nil {
		return
	}
	e.exporter.ExportRPCMuxDiagnosisEvent(ctx, RPCMuxDiagnosisEventRecord{
		Event:      record.Event,
		ExportedAt: record.Timestamp,
	})
}

func (e rpcMuxSubprocessOTelLogExporter) RPCMuxSubprocessExporterSnapshot() RPCMuxSubprocessExporterSnapshot {
	snapshotter, ok := e.exporter.(RPCMuxSubprocessExporterSnapshotter)
	if !ok {
		return RPCMuxSubprocessExporterSnapshot{}
	}
	return snapshotter.RPCMuxSubprocessExporterSnapshot()
}

func normalizeRPCMuxSubprocessExporterConfig(config RPCMuxSubprocessExporterConfig) (RPCMuxSubprocessExporterConfig, error) {
	policy, err := config.ExecutionPolicy().Validate()
	if err != nil {
		return RPCMuxSubprocessExporterConfig{}, err
	}
	config.Command = policy.Command
	config.Args = policy.Args
	config.WorkDir = policy.WorkDir
	config.WorkDirRoot = policy.WorkDirRoot
	config.AllowCommands = policy.AllowCommands
	config.DenyCommands = policy.DenyCommands
	config.Env = policy.Env
	config.EnvWhitelist = policy.EnvWhitelist
	if config.Timeout <= 0 {
		config.Timeout = defaultRPCMuxSubprocessTimeout
	}
	if config.MaxOutputBytes <= 0 {
		config.MaxOutputBytes = defaultRPCMuxSubprocessMaxOutputBytes
	}
	return config, nil
}

func (p RPCMuxSubprocessExecutionPolicy) Validate() (RPCMuxSubprocessExecutionPolicy, error) {
	p.Command = strings.TrimSpace(p.Command)
	if p.Command == "" {
		return RPCMuxSubprocessExecutionPolicy{}, fmt.Errorf("subprocess exporter command is required")
	}
	if strings.ContainsAny(p.Command, "\x00\r\n") {
		return RPCMuxSubprocessExecutionPolicy{}, fmt.Errorf("subprocess exporter command contains invalid characters")
	}
	if len(p.Args) > defaultRPCMuxSubprocessMaxArgs {
		return RPCMuxSubprocessExecutionPolicy{}, fmt.Errorf("subprocess exporter args exceed %d", defaultRPCMuxSubprocessMaxArgs)
	}
	args := make([]string, 0, len(p.Args))
	for _, arg := range p.Args {
		if strings.ContainsAny(arg, "\x00\r\n") {
			return RPCMuxSubprocessExecutionPolicy{}, fmt.Errorf("subprocess exporter arg contains invalid characters")
		}
		args = append(args, arg)
	}
	p.Args = args
	p.AllowCommands = normalizeStringList(p.AllowCommands)
	p.DenyCommands = normalizeStringList(p.DenyCommands)
	if len(p.AllowCommands) > 0 && !stringListContains(p.AllowCommands, p.Command) {
		return RPCMuxSubprocessExecutionPolicy{}, newRPCMuxSubprocessPolicyError(RPCMuxSubprocessPolicyErrorCommandDenied, "subprocess exporter command is not allowed")
	}
	if stringListContains(p.DenyCommands, p.Command) {
		return RPCMuxSubprocessExecutionPolicy{}, newRPCMuxSubprocessPolicyError(RPCMuxSubprocessPolicyErrorCommandDenied, "subprocess exporter command is denied")
	}
	p.WorkDir = strings.TrimSpace(p.WorkDir)
	p.WorkDirRoot = strings.TrimSpace(p.WorkDirRoot)
	if p.WorkDir != "" {
		if p.WorkDirRoot == "" {
			return RPCMuxSubprocessExecutionPolicy{}, fmt.Errorf("subprocess exporter workDirRoot is required")
		}
		root := filepath.Clean(p.WorkDirRoot)
		if !filepath.IsAbs(root) {
			return RPCMuxSubprocessExecutionPolicy{}, fmt.Errorf("subprocess exporter workDirRoot must be absolute")
		}
		if !filepath.IsLocal(p.WorkDir) {
			return RPCMuxSubprocessExecutionPolicy{}, newRPCMuxSubprocessPolicyError(RPCMuxSubprocessPolicyErrorWorkDirEscaped, "subprocess exporter workDir must be local")
		}
		workDir := filepath.Join(root, p.WorkDir)
		rel, err := filepath.Rel(root, workDir)
		if err != nil || !filepath.IsLocal(rel) {
			return RPCMuxSubprocessExecutionPolicy{}, newRPCMuxSubprocessPolicyError(RPCMuxSubprocessPolicyErrorWorkDirEscaped, "subprocess exporter workDir escapes root")
		}
		p.WorkDirRoot = root
		p.WorkDir = workDir
	}
	env, err := normalizeSubprocessEnv(p.Env, p.EnvWhitelist)
	if err != nil {
		return RPCMuxSubprocessExecutionPolicy{}, err
	}
	p.Env = env
	p.EnvWhitelist = normalizeStringList(p.EnvWhitelist)
	return p, nil
}

func newRPCMuxSubprocessPolicyError(category string, message string) RPCMuxSubprocessExecutionPolicyError {
	return RPCMuxSubprocessExecutionPolicyError{Category: category, Message: message}
}

func normalizeStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func stringListContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func normalizeSubprocessEnv(env map[string]string, whitelist []string) (map[string]string, error) {
	if len(env) == 0 {
		return nil, nil
	}
	allowed := map[string]struct{}{}
	for _, key := range normalizeStringList(whitelist) {
		allowed[key] = struct{}{}
	}
	if len(allowed) == 0 {
		return nil, newRPCMuxSubprocessPolicyError(RPCMuxSubprocessPolicyErrorEnvNotWhitelisted, "subprocess exporter envWhitelist is required when env is set")
	}
	out := make(map[string]string, len(env))
	for key, value := range env {
		key = strings.TrimSpace(key)
		if key == "" || strings.ContainsAny(key, "=\x00\r\n") {
			return nil, fmt.Errorf("subprocess exporter env key is invalid")
		}
		if _, ok := allowed[key]; !ok {
			return nil, newRPCMuxSubprocessPolicyError(RPCMuxSubprocessPolicyErrorEnvNotWhitelisted, fmt.Sprintf("subprocess exporter env key %q is not whitelisted", key))
		}
		if strings.ContainsAny(value, "\x00") {
			return nil, fmt.Errorf("subprocess exporter env value contains invalid characters")
		}
		out[key] = value
	}
	return out, nil
}

func subprocessExecutionPolicySnapshot(config RPCMuxSubprocessExporterConfig) RPCMuxSubprocessExecutionPolicySnapshot {
	return RPCMuxSubprocessExecutionPolicySnapshot{
		AllowCommands: append([]string(nil), config.AllowCommands...),
		DenyCommands:  append([]string(nil), config.DenyCommands...),
		WorkDirRoot:   config.WorkDirRoot,
		EnvWhitelist:  append([]string(nil), config.EnvWhitelist...),
		EnvCount:      len(config.Env),
	}
}

func (e *rpcMuxSubprocessExporter) ExportRPCMuxDiagnosisEvent(ctx context.Context, record RPCMuxDiagnosisEventRecord) {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := e.config.Timeout
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	startedAt := time.Now()
	payload, err := json.Marshal(record)
	if err != nil {
		e.recordRun(-1, time.Since(startedAt), false, false, err)
		return
	}
	// #nosec G204 G702 -- command, args, env, and workDir are normalized through RPCMuxSubprocessExecutionPolicy before exporter construction.
	cmd := exec.CommandContext(runCtx, e.config.Command, e.config.Args...)
	cmd.Dir = e.config.WorkDir
	if len(e.config.Env) > 0 {
		cmd.Env = append(os.Environ(), subprocessEnvList(e.config.Env)...)
	}
	cmd.Stdin = bytes.NewReader(payload)
	var output bytes.Buffer
	writer := &limitWriter{w: &output, max: e.config.MaxOutputBytes}
	cmd.Stdout = writer
	cmd.Stderr = writer
	err = cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	e.recordRun(exitCode, time.Since(startedAt), runCtx.Err() == context.DeadlineExceeded, writer.truncated, err)
}

func subprocessEnvList(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for key, value := range env {
		out = append(out, key+"="+value)
	}
	return out
}

func (e *rpcMuxSubprocessExporter) recordRun(exitCode int, duration time.Duration, timedOut bool, truncated bool, err error) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.snapshot.Runs++
	e.snapshot.LastExitCode = exitCode
	e.snapshot.LastDuration = duration
	e.snapshot.LastTimedOut = timedOut
	e.snapshot.LastOutputTruncated = truncated
	e.snapshot.LastRunAt = time.Now().UTC()
	if err != nil {
		e.snapshot.LastError = err.Error()
	} else {
		e.snapshot.LastError = ""
	}
}

func (e *rpcMuxSubprocessExporter) RPCMuxSubprocessExporterSnapshot() RPCMuxSubprocessExporterSnapshot {
	if e == nil {
		return RPCMuxSubprocessExporterSnapshot{}
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.snapshot
}

type limitWriter struct {
	w         io.Writer
	max       int64
	n         int64
	truncated bool
}

func (w *limitWriter) Write(p []byte) (int, error) {
	if w == nil {
		return len(p), nil
	}
	if w.w == nil || w.max <= 0 {
		return len(p), nil
	}
	remaining := w.max - w.n
	if remaining <= 0 {
		w.truncated = true
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		_, _ = w.w.Write(p[:remaining])
		w.n += remaining
		w.truncated = true
		return len(p), nil
	}
	n, _ := w.w.Write(p)
	w.n += int64(n)
	return len(p), nil
}

var _ RPCMuxDiagnosisEventExporter = (*rpcMuxSubprocessExporter)(nil)
