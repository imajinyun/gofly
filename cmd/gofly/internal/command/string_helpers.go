package command

import "strings"

func parseKeyValueCSV(value string) map[string]string {
	parts := strings.Split(value, ",")
	out := map[string]string{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, val, ok := strings.Cut(part, "=")
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if !ok {
			out[key] = ""
			continue
		}
		out[key] = strings.TrimSpace(val)
	}
	return out
}
