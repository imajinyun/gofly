package command

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
	"github.com/imajinyun/gofly/rpc"
)

func rpcDescriptorCommand(args []string) error {
	fs := flag.NewFlagSet("rpc descriptor", flag.ContinueOnError)
	base := fs.String("base", "", "base descriptor json file")
	target := fs.String("target", "", "target descriptor json file")
	remoteURL := fs.String("url", "", "remote admin descriptor URL or admin base URL")
	service := fs.String("service", "", "service name when --url points at an admin base URL")
	formatName := fs.String("format", "text", "output format: text or json")
	token := fs.String("token", "", "bearer token for descriptor URL sources")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *base == "" && len(remaining) > 0 {
		*base = remaining[0]
		remaining = remaining[1:]
	}
	if *target == "" && len(remaining) > 0 {
		*target = remaining[0]
	}
	if *remoteURL != "" {
		if *base == "" {
			*base = *remoteURL
		} else if *target == "" {
			*target = *remoteURL
		}
	}
	if *base == "" || *target == "" {
		return fmt.Errorf("%w: base and target descriptor sources are required", errUsage)
	}
	baseDescriptor, err := readRPCDescriptorSource(*base, *token, *service)
	if err != nil {
		return fmt.Errorf("read base descriptor: %w", err)
	}
	targetDescriptor, err := readRPCDescriptorSource(*target, *token, *service)
	if err != nil {
		return fmt.Errorf("read target descriptor: %w", err)
	}
	report := rpc.CompareDescriptors(baseDescriptor, targetDescriptor)
	switch strings.ToLower(strings.TrimSpace(*formatName)) {
	case "", "text":
		cliOutput(formatRPCDescriptorCompatibilityText(report))
	case "json":
		if err := printJSON(report); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: unsupported rpc descriptor format %q", errUsage, *formatName)
	}
	if report.HasBreaking() {
		return generator.ErrBreakingChanges
	}
	return nil
}

func readRPCDescriptorSource(source string, token string, service string) (rpc.Descriptor, error) {
	if descriptorSourceIsURL(source) {
		return readRPCDescriptorURL(source, token, service)
	}
	return readRPCDescriptorFile(source)
}

func descriptorSourceIsURL(source string) bool {
	lower := strings.ToLower(strings.TrimSpace(source))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func readRPCDescriptorURL(source string, token string, service string) (rpc.Descriptor, error) {
	parsed, err := url.Parse(source)
	if err != nil {
		return rpc.Descriptor{}, err
	}
	scheme := strings.ToLower(parsed.Scheme)
	if parsed.Host == "" || (scheme != "http" && scheme != "https") {
		return rpc.Descriptor{}, fmt.Errorf("unsupported descriptor URL %q", source)
	}
	if err := normalizeRPCDescriptorURL(parsed, service); err != nil {
		return rpc.Descriptor{}, err
	}
	client := http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, parsed.String(), nil)
	if err != nil {
		return rpc.Descriptor{}, err
	}
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	resp, err := client.Do(req)
	if err != nil {
		return rpc.Descriptor{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return rpc.Descriptor{}, fmt.Errorf("descriptor endpoint returned status %d", resp.StatusCode)
	}
	return decodeRPCDescriptor(resp.Body)
}

func normalizeRPCDescriptorURL(parsed *url.URL, service string) error {
	plainPath := strings.TrimRight(parsed.Path, "/")
	service = strings.TrimSpace(service)
	switch {
	case strings.Contains(plainPath, "/rpc/admin/descriptors/"):
		return nil
	case strings.HasSuffix(plainPath, "/rpc/admin/descriptors"):
		if service == "" {
			return fmt.Errorf("--service is required when descriptor URL points at %s", parsed.String())
		}
		parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + url.PathEscape(service)
		return nil
	case service != "":
		parsed.Path = strings.TrimRight(parsed.Path, "/") + "/rpc/admin/descriptors/" + url.PathEscape(service)
		return nil
	case strings.HasSuffix(plainPath, "/admin"):
		return fmt.Errorf("--service is required when descriptor URL points at admin base %s", parsed.String())
	default:
		return nil
	}
}

func readRPCDescriptorFile(path string) (rpc.Descriptor, error) {
	// #nosec G304 -- descriptor comparison reads explicit descriptor JSON files supplied to the CLI.
	f, err := os.Open(path)
	if err != nil {
		return rpc.Descriptor{}, err
	}
	defer func() { _ = f.Close() }()
	return decodeRPCDescriptor(f)
}

func decodeRPCDescriptor(r io.Reader) (rpc.Descriptor, error) {
	var descriptor rpc.Descriptor
	if err := json.NewDecoder(r).Decode(&descriptor); err != nil {
		return rpc.Descriptor{}, err
	}
	if err := descriptor.Validate(); err != nil {
		return rpc.Descriptor{}, err
	}
	return descriptor, nil
}

func formatRPCDescriptorCompatibilityText(report rpc.DescriptorCompatibilityReport) string {
	if len(report.Changes) == 0 {
		return "No breaking changes\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Descriptor compatibility: %d breaking, %d warning(s), %d change(s)\n", report.Breaking, report.Warnings, len(report.Changes))
	for _, change := range report.Changes {
		fmt.Fprintf(&b, "[%s] %s %s: %s\n", change.Severity, change.Category, change.Subject, change.Description)
	}
	return b.String()
}
