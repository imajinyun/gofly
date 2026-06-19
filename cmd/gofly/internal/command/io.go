package command

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

const (
	outputText = "text"
	outputJSON = "json"
)

// Output verbosity modes.
const (
	verbosityNormal  = 0
	verbosityQuiet   = -1
	verbosityVerbose = 1
)

type IOStreams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

type commandIOState struct {
	streams   IOStreams
	output    string
	verbosity int
}

var (
	commandIOMu sync.Mutex
	commandIO   = commandIOState{output: outputText}
)

func defaultIOStreams() IOStreams {
	return IOStreams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
}

func normalizeIOStreams(streams IOStreams) IOStreams {
	defaults := defaultIOStreams()
	if streams.In == nil {
		streams.In = defaults.In
	}
	if streams.Out == nil {
		streams.Out = defaults.Out
	}
	if streams.Err == nil {
		streams.Err = defaults.Err
	}
	return streams
}

func cliOutput(args ...any) {
	_, _ = fmt.Fprint(currentOut(), args...)
}

func cliOutputf(format string, args ...any) {
	_, _ = fmt.Fprintf(currentOut(), format, args...)
}

func cliOutputln(args ...any) {
	_, _ = fmt.Fprintln(currentOut(), args...)
}

func errorf(format string, args ...any) {
	_, _ = fmt.Fprintf(currentErr(), format, args...)
}

func currentOut() io.Writer {
	if commandIO.streams.Out != nil {
		return commandIO.streams.Out
	}
	return os.Stdout
}

func currentErr() io.Writer {
	if commandIO.streams.Err != nil {
		return commandIO.streams.Err
	}
	return os.Stderr
}

// OutputMode returns the current output mode ("text" or "json").
func OutputMode() string {
	if commandIO.output == outputJSON {
		return outputJSON
	}
	return outputText
}

// outputMode is an alias for OutputMode used by internal callers.
func outputMode() string { return OutputMode() }

// isQuiet returns true when output is suppressed to stderr-only mode.
func isQuiet() bool { return commandIO.verbosity <= verbosityQuiet }

// isVerbose returns true when extra diagnostic output is requested.
func isVerbose() bool { return commandIO.verbosity >= verbosityVerbose }

// cliOutputIf prints args to stdout unless quiet mode is active.
func cliOutputIf(args ...any) {
	if !isQuiet() {
		cliOutput(args...)
	}
}

// cliOutputfIf prints a formatted message to stdout unless quiet.
func cliOutputfIf(format string, args ...any) {
	if !isQuiet() {
		cliOutputf(format, args...)
	}
}

// cliOutputlnIf prints a line to stdout unless quiet.
func cliOutputlnIf(args ...any) {
	if !isQuiet() {
		cliOutputln(args...)
	}
}

// verboseOutputf prints a formatted message to stderr when verbose mode is on.
func verboseOutputf(format string, args ...any) {
	if isVerbose() {
		_, _ = fmt.Fprintf(currentErr(), format, args...)
	}
}

// setVerbosity updates the global verbosity level for the current command.
// This is used by command implementations that parse --verbose/--quiet
// flags locally rather than globally.
func setVerbosity(level int) {
	commandIOMu.Lock()
	commandIO.verbosity = level
	commandIOMu.Unlock()
}

// withCommandIO sets up I/O streams, output mode, and verbosity for the
// duration of fn.
func withCommandIO(streams IOStreams, output string, verbosity int, fn func() error) error {
	commandIOMu.Lock()
	previous := commandIO
	commandIO = commandIOState{
		streams:   normalizeIOStreams(streams),
		output:    normalizeOutputMode(output),
		verbosity: verbosity,
	}
	commandIOMu.Unlock()
	defer func() {
		commandIOMu.Lock()
		commandIO = previous
		commandIOMu.Unlock()
	}()
	return fn()
}

// resolveVerbosity computes a verbosity level from optional boolean flags.
// More than one truthy value picks the most extreme.
func resolveVerbosity(verbose, v, quiet, q *bool) int {
	switch {
	case (quiet != nil && *quiet) || (q != nil && *q):
		return verbosityQuiet
	case (verbose != nil && *verbose) || (v != nil && *v):
		return verbosityVerbose
	default:
		return verbosityNormal
	}
}

func normalizeOutputMode(output string) string {
	switch output {
	case "", outputText:
		return outputText
	case outputJSON:
		return outputJSON
	default:
		return output
	}
}

// errorCodeClass returns a short machine-readable label for an error,
// suitable for use in JSON error envelopes.
func errorCodeClass(err error) string {
	if err == nil {
		return "OK"
	}
	if ExitCode(err) == exitUsage {
		return "USAGE_ERROR"
	}
	return "INTERNAL_ERROR"
}

// WriteErrorJSON writes a structured JSON error envelope to w. The envelope
// includes a machine-readable code and the error message. It is a no-op
// if err is nil.
func WriteErrorJSON(w io.Writer, err error) {
	if err == nil {
		return
	}
	type errorEnvelope struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_ = json.NewEncoder(w).Encode(struct {
		Error errorEnvelope `json:"error"`
	}{
		Error: errorEnvelope{
			Code:    errorCodeClass(err),
			Message: err.Error(),
		},
	})
}
