package generator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	core "github.com/gofly/gofly/core"
)

type ProtocOptions struct {
	ProtoFile    string
	ProtoPath    []string
	GoOut        string
	GoGRPCOut    string
	GoflyOut     string
	GoflyPlugin  string
	GoflyOptions []string
	Protoc       string
	ExtraArgs    []string
	Env          []string
	Timeout      time.Duration
}

func GenerateStandardProto(ctx context.Context, opts ProtocOptions) error {
	args, err := ProtocArgs(opts)
	if err != nil {
		return err
	}
	ctx, cancel := protocContext(ctx, opts.Timeout)
	defer cancel()
	bin := opts.Protoc
	if bin == "" {
		bin = "protoc"
	}
	// #nosec G204 -- protoc execution is an explicit generator feature; args are constructed as argv entries and never shell-expanded.
	cmd := exec.CommandContext(ctx, bin, args...)
	configureProtocCommand(cmd)
	cmd.WaitDelay = 5 * time.Second
	if len(opts.Env) > 0 {
		cmd.Env = append(os.Environ(), opts.Env...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			if errors.Is(ctxErr, context.DeadlineExceeded) && opts.Timeout > 0 {
				return fmt.Errorf("run protoc timed out after %s: %w: %s", opts.Timeout, ctxErr, out)
			}
			return fmt.Errorf("run protoc: %w: %s", ctxErr, out)
		}
		return fmt.Errorf("run protoc: %w: %s", err, out)
	}
	return nil
}

func protocContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx = core.Context(ctx)
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func ProtocArgs(opts ProtocOptions) ([]string, error) {
	if opts.ProtoFile == "" {
		return nil, errors.New("proto file is required")
	}
	goOut := opts.GoOut
	if goOut == "" {
		goOut = "."
	}
	goGRPCOut := opts.GoGRPCOut
	if goGRPCOut == "" {
		goGRPCOut = goOut
	}
	args := make([]string, 0, 6+len(opts.ProtoPath)+len(opts.ExtraArgs))
	for _, path := range opts.ProtoPath {
		if path != "" {
			args = append(args, "-I", path)
		}
	}
	args = append(args, "--go_out="+goOut)
	if !hasProtocOptionOverride(opts.ExtraArgs, "--go_opt") {
		args = append(args, "--go_opt=paths=source_relative")
	}
	args = append(args, "--go-grpc_out="+goGRPCOut)
	if !hasProtocOptionOverride(opts.ExtraArgs, "--go-grpc_opt") {
		args = append(args, "--go-grpc_opt=paths=source_relative")
	}
	if opts.GoflyOut != "" {
		if opts.GoflyPlugin != "" {
			args = append(args, "--plugin=protoc-gen-gofly="+opts.GoflyPlugin)
		}
		args = append(args, "--gofly_out="+opts.GoflyOut)
		for _, opt := range opts.GoflyOptions {
			if opt != "" {
				args = append(args, "--gofly_opt="+opt)
			}
		}
	}
	args = append(args, opts.ExtraArgs...)
	args = append(args, opts.ProtoFile)
	return args, nil
}

func hasProtocOptionOverride(args []string, flagName string) bool {
	for _, arg := range args {
		value, ok := strings.CutPrefix(arg, flagName+"=")
		if !ok {
			continue
		}
		if strings.HasPrefix(value, "paths=") || strings.HasPrefix(value, "module=") {
			return true
		}
	}
	return false
}
