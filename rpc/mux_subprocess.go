package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

const (
	defaultRPCMuxSubprocessTimeout        = time.Second
	defaultRPCMuxSubprocessMaxArgs        = 16
	defaultRPCMuxSubprocessMaxOutputBytes = 64 << 10
)

// RPCMuxSubprocessExporterConfig configures a local subprocess exporter. The
// executable and arguments are passed directly to exec.CommandContext; shell
// command strings are intentionally unsupported.
type RPCMuxSubprocessExporterConfig struct {
	Command        string        `json:"command"`
	Args           []string      `json:"args,omitempty"`
	Timeout        time.Duration `json:"timeout,omitempty"`
	MaxOutputBytes int64         `json:"maxOutputBytes,omitempty"`
}

type rpcMuxSubprocessExporter struct {
	config RPCMuxSubprocessExporterConfig
}

type rpcMuxSubprocessOTelLogSinkProvider struct{}

// NewRPCMuxSubprocessDiagnosisEventExporter creates a local subprocess exporter
// that writes one JSON event envelope to stdin per export call.
func NewRPCMuxSubprocessDiagnosisEventExporter(config RPCMuxSubprocessExporterConfig) (RPCMuxDiagnosisEventExporter, error) {
	config, err := normalizeRPCMuxSubprocessExporterConfig(config)
	if err != nil {
		return nil, err
	}
	return rpcMuxSubprocessExporter{config: config}, nil
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
	return json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false,"required":["command"],"properties":{"command":{"type":"string","minLength":1},"args":{"type":"array","maxItems":16,"items":{"type":"string"}},"timeout":{"type":"integer","minimum":1},"maxOutputBytes":{"type":"integer","minimum":1}}}`)
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

func normalizeRPCMuxSubprocessExporterConfig(config RPCMuxSubprocessExporterConfig) (RPCMuxSubprocessExporterConfig, error) {
	config.Command = strings.TrimSpace(config.Command)
	if config.Command == "" {
		return RPCMuxSubprocessExporterConfig{}, fmt.Errorf("subprocess exporter command is required")
	}
	if strings.ContainsAny(config.Command, "\x00\r\n") {
		return RPCMuxSubprocessExporterConfig{}, fmt.Errorf("subprocess exporter command contains invalid characters")
	}
	if len(config.Args) > defaultRPCMuxSubprocessMaxArgs {
		return RPCMuxSubprocessExporterConfig{}, fmt.Errorf("subprocess exporter args exceed %d", defaultRPCMuxSubprocessMaxArgs)
	}
	args := make([]string, 0, len(config.Args))
	for _, arg := range config.Args {
		if strings.ContainsAny(arg, "\x00\r\n") {
			return RPCMuxSubprocessExporterConfig{}, fmt.Errorf("subprocess exporter arg contains invalid characters")
		}
		args = append(args, arg)
	}
	config.Args = args
	if config.Timeout <= 0 {
		config.Timeout = defaultRPCMuxSubprocessTimeout
	}
	if config.MaxOutputBytes <= 0 {
		config.MaxOutputBytes = defaultRPCMuxSubprocessMaxOutputBytes
	}
	return config, nil
}

func (e rpcMuxSubprocessExporter) ExportRPCMuxDiagnosisEvent(ctx context.Context, record RPCMuxDiagnosisEventRecord) {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := e.config.Timeout
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	payload, err := json.Marshal(record)
	if err != nil {
		return
	}
	cmd := exec.CommandContext(runCtx, e.config.Command, e.config.Args...)
	cmd.Stdin = bytes.NewReader(payload)
	var output bytes.Buffer
	writer := &limitWriter{w: &output, max: e.config.MaxOutputBytes}
	cmd.Stdout = writer
	cmd.Stderr = writer
	_ = cmd.Run()
}

type limitWriter struct {
	w   io.Writer
	max int64
	n   int64
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
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		_, _ = w.w.Write(p[:remaining])
		w.n += remaining
		return len(p), nil
	}
	n, _ := w.w.Write(p)
	w.n += int64(n)
	return len(p), nil
}

var _ RPCMuxDiagnosisEventExporter = rpcMuxSubprocessExporter{}
