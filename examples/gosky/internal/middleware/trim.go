package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/imajinyun/gofly/rest"
)

func TrimSpaceMiddleware() rest.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			trimQuery(r)
			trimBody(r)
			next.ServeHTTP(w, r)
		})
	}
}

func trimQuery(r *http.Request) {
	q := r.URL.Query()
	for key, values := range q {
		for i, value := range values {
			values[i] = strings.TrimSpace(value)
		}
		q[key] = values
	}
	r.URL.RawQuery = q.Encode()
}

func trimBody(r *http.Request) {
	if r.Body == nil || r.Body == http.NoBody {
		return
	}
	data, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(nil))
		return
	}
	data = bytes.TrimSpace(data)
	if isJSON(r.Header.Get("Content-Type")) && len(data) > 0 {
		data = trimJSONBody(data)
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	r.ContentLength = int64(len(data))
	r.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
}

func isJSON(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "application/json")
}

func trimJSONBody(data []byte) []byte {
	var payload any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return data
	}
	trimmed, err := json.Marshal(trimJSONValue(payload))
	if err != nil {
		return data
	}
	return trimmed
}

func trimJSONValue(v any) any {
	switch value := v.(type) {
	case string:
		return strings.TrimSpace(value)
	case []any:
		for i, item := range value {
			value[i] = trimJSONValue(item)
		}
		return value
	case map[string]any:
		for key, item := range value {
			value[key] = trimJSONValue(item)
		}
		return value
	default:
		return value
	}
}
