package command

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/imajinyun/gofly/rpc"
)

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
