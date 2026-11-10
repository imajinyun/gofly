package app

import (
	"regexp"
	"strings"

	coreerrors "github.com/imajinyun/gofly/core/errors"
)

// storageKeyPattern matches the keys minted by the upload handler: 32 hex
// characters plus an optional lowercase file extension (see storageKey).
var storageKeyPattern = regexp.MustCompile(`^[0-9a-f]{32}(?:\.[a-z0-9]{1,9})?$`)

// normalizeAttachmentKeys validates browser-supplied file references and
// returns their de-duplicated, order-preserving server keys. Keys are
// server-generated upload handles, never user-chosen paths, so an unexpected
// shape is rejected rather than persisted.
func normalizeAttachmentKeys(keys []string) ([]string, *coreerrors.Error) {
	return normalizeAttachmentKeysWithLimit(keys, maxFeedbackAttachments)
}

// normalizeAttachmentKeysWithLimit enforces a caller-specific key count and
// the database column budget: keys are joined with commas, so the resulting
// string must fit within maxJoinedLength characters.
func normalizeAttachmentKeysWithLimit(keys []string, maxKeys int) ([]string, *coreerrors.Error) {
	if maxKeys <= 0 || len(keys) > maxKeys {
		return nil, coreerrors.New(coreerrors.CodeInvalidArgument, "too many attachments")
	}
	normalized := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, raw := range keys {
		key := strings.TrimSpace(raw)
		if key == "" {
			continue
		}
		if len(key) > 255 || !storageKeyPattern.MatchString(key) {
			return nil, coreerrors.New(coreerrors.CodeInvalidArgument, "attachment key is invalid")
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, key)
	}
	if joined := strings.Join(normalized, ","); len(joined) > maxAttachmentJoinedLength {
		return nil, coreerrors.New(coreerrors.CodeInvalidArgument, "attachment list is too long")
	}
	return normalized, nil
}
