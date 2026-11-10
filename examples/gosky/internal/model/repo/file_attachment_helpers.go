package repo

import (
	"context"
	"strings"

	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
)

// splitAttachmentKeys splits the comma-joined upload-key column used by
// feedback and help documents.
func splitAttachmentKeys(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	keys := make([]string, 0, len(parts))
	for _, part := range parts {
		if key := strings.TrimSpace(part); key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

// loadFileMetadataByKeys loads support_files metadata for every referenced
// key in one parameterized IN query; missing keys are simply absent from the
// map, so deleting an upload never breaks a feedback/doc projection.
func loadFileMetadataByKeys(ctx context.Context, store *storage.SQLStore, tenantID uint64, keys []string) (map[string]app.FeedbackAttachment, error) {
	result := make(map[string]app.FeedbackAttachment, len(keys))
	if len(keys) == 0 {
		return result, nil
	}
	args := make([]any, 0, len(keys)+1)
	args = append(args, tenantID)
	for _, key := range keys {
		args = append(args, key)
	}
	rows, err := store.QueryRows(ctx,
		`SELECT file_id, file_name, file_key, file_size, file_type FROM support_files
WHERE tenant_id = ? AND file_key IN (`+placeholdersFor(make([]uint64, len(keys)))+`)`,
		args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var attachment app.FeedbackAttachment
		if err := rows.Scan(&attachment.FileID, &attachment.FileName, &attachment.FileKey,
			&attachment.FileSize, &attachment.FileType); err != nil {
			return nil, err
		}
		result[attachment.FileKey] = attachment
	}
	return result, rows.Err()
}

// resolveInKeyOrder expands stored keys into attachments in stored order.
func resolveInKeyOrder(keys []string, files map[string]app.FeedbackAttachment) []app.FeedbackAttachment {
	attachments := make([]app.FeedbackAttachment, 0, len(keys))
	for _, key := range keys {
		if attachment, ok := files[key]; ok {
			attachments = append(attachments, attachment)
		}
	}
	return attachments
}
