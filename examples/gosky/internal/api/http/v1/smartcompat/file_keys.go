// Package smartcompat holds transport decoders shared by the unmodified
// SmartAdmin support endpoints.
package smartcompat

import (
	"bytes"
	"encoding/json"
	"strings"

	coreerrors "github.com/imajinyun/gofly/core/errors"
)

// FileKeys accepts the upload component's FileVO array (and a single object
// or comma-joined raw string, matching SmartAdmin's FileKeyVoDeserializer)
// and extracts the server-generated file keys. Validation of the key shape
// happens in the app layer.
type FileKeys struct {
	Keys []string
}

// UnmarshalJSON decodes [], {}, "", and null inputs.
func (f *FileKeys) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	switch trimmed[0] {
	case '"':
		var raw string
		if err := json.Unmarshal(trimmed, &raw); err != nil {
			return err
		}
		f.Keys = splitNonEmpty(raw, ",")
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return err
		}
		for _, item := range items {
			key, err := DecodeFileKey(item)
			if err != nil {
				return err
			}
			if key != "" {
				f.Keys = append(f.Keys, key)
			}
		}
	case '{':
		key, err := DecodeFileKey(trimmed)
		if err != nil {
			return err
		}
		if key != "" {
			f.Keys = append(f.Keys, key)
		}
	default:
		return coreerrors.New(coreerrors.CodeInvalidArgument, "attachment value is invalid")
	}
	return nil
}

// DecodeFileKey extracts fileKey from one FileVO raw value or a JSON string.
func DecodeFileKey(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", nil
	}
	if trimmed[0] == '"' {
		var key string
		if err := json.Unmarshal(trimmed, &key); err != nil {
			return "", err
		}
		return key, nil
	}
	var file struct {
		FileKey string `json:"fileKey"`
	}
	if err := json.Unmarshal(trimmed, &file); err != nil {
		return "", coreerrors.New(coreerrors.CodeInvalidArgument, "attachment file is invalid")
	}
	return file.FileKey, nil
}

func splitNonEmpty(raw, separator string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, separator)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if key := strings.TrimSpace(part); key != "" {
			out = append(out, key)
		}
	}
	return out
}
