package repo

import (
	"context"
	"strings"

	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
)

var _ app.FeedbackStore = (*FeedbackRepository)(nil)

// FeedbackRepository persists employee feedback. The submitter is resolved
// from the verified subject through an active same-tenant membership;
// attachment keys are expanded to support_files metadata in a second
// parameterized query so the console can render thumbnails.
type FeedbackRepository struct {
	store *storage.SQLStore
}

func NewFeedbackRepository(store *storage.SQLStore) *FeedbackRepository {
	return &FeedbackRepository{store: store}
}

const feedbackColumns = `feedback_id, user_id, user_type, user_name, feedback_content,
feedback_attachment, created_at, updated_at`

// Add inserts one row with the submitter resolved from the subject.
func (r *FeedbackRepository) Add(ctx context.Context, tenantID uint64, subject string, input app.FeedbackInput) error {
	query := `INSERT INTO support_feedbacks
(tenant_id, user_id, user_type, user_name, feedback_content, feedback_attachment)
SELECT ?, u.user_id, ?, COALESCE(NULLIF(tu.display_name, ''), u.display_name), ?, ?
FROM users AS u
JOIN tenant_users AS tu ON tu.user_id = u.user_id
WHERE tu.tenant_id = ? AND u.subject = ?
  AND u.user_state = 'active' AND tu.member_state = 'active'`
	result, err := r.store.Exec(ctx, query,
		tenantID, app.FeedbackUserTypeEmployee, input.Content, strings.Join(input.AttachmentKeys, ","),
		tenantID, subject)
	if err != nil {
		return err
	}
	return requireRows(result)
}

// List returns a tenant-scoped page with attachment file metadata expanded.
func (r *FeedbackRepository) List(ctx context.Context, tenantID uint64, query app.FeedbackQuery) (app.FeedbackPage, error) {
	where := []string{"tenant_id = ?"}
	args := []any{tenantID}
	if value := strings.TrimSpace(query.SearchWord); value != "" {
		like := "%" + value + "%"
		where = append(where, "(feedback_content LIKE ? OR user_name LIKE ?)")
		args = append(args, like, like)
	}
	if value := strings.TrimSpace(query.StartDate); value != "" {
		where = append(where, "created_at >= ?")
		args = append(args, value+" 00:00:00")
	}
	if value := strings.TrimSpace(query.EndDate); value != "" {
		where = append(where, "created_at <= ?")
		args = append(args, value+" 23:59:59")
	}
	filter := strings.Join(where, " AND ")

	var total int
	countRows, err := r.store.QueryRows(ctx, `SELECT COUNT(*) FROM support_feedbacks WHERE `+filter, args...)
	if err != nil {
		return app.FeedbackPage{}, err
	}
	if countRows.Next() {
		if err := countRows.Scan(&total); err != nil {
			_ = countRows.Close()
			return app.FeedbackPage{}, err
		}
	}
	_ = countRows.Close()

	pageArgs := append(append([]any{}, args...), query.PageSize, (query.PageNum-1)*query.PageSize)
	rows, err := r.store.QueryRows(ctx,
		`SELECT `+feedbackColumns+` FROM support_feedbacks WHERE `+filter+
			` ORDER BY created_at DESC, feedback_id DESC LIMIT ? OFFSET ?`,
		pageArgs...)
	if err != nil {
		return app.FeedbackPage{}, err
	}
	defer func() { _ = rows.Close() }()

	items := make([]app.Feedback, 0, query.PageSize)
	keyOrder := make([]uint64, 0, query.PageSize)
	rawKeys := make(map[uint64][]string)
	for rows.Next() {
		item, scanErr := scanFeedback(rows)
		if scanErr != nil {
			return app.FeedbackPage{}, scanErr
		}
		keys := splitAttachmentKeys(item.attachmentKeys)
		rawKeys[item.ID] = keys
		keyOrder = append(keyOrder, item.ID)
		items = append(items, item.Feedback)
	}
	if err := rows.Err(); err != nil {
		return app.FeedbackPage{}, err
	}

	filesByKey, err := loadFileMetadataByKeys(ctx, r.store, tenantID, uniqueFeedbackKeys(rawKeys))
	if err != nil {
		return app.FeedbackPage{}, err
	}
	for _, id := range keyOrder {
		for index := range items {
			if items[index].ID != id {
				continue
			}
			items[index].Attachments = resolveInKeyOrder(rawKeys[id], filesByKey)
			break
		}
	}
	return app.FeedbackPage{List: items, Total: total}, nil
}

func uniqueFeedbackKeys(byFeedback map[uint64][]string) []string {
	unique := make(map[string]struct{})
	for _, keys := range byFeedback {
		for _, key := range keys {
			unique[key] = struct{}{}
		}
	}
	out := make([]string, 0, len(unique))
	for key := range unique {
		out = append(out, key)
	}
	return out
}

type feedbackScan struct {
	app.Feedback
	attachmentKeys string
}

func scanFeedback(scanner interface{ Scan(dest ...any) error }) (feedbackScan, error) {
	var row feedbackScan
	if err := scanner.Scan(&row.ID, &row.UserID, &row.UserType, &row.UserName, &row.Content,
		&row.attachmentKeys, &row.CreatedAt, &row.UpdatedAt); err != nil {
		return feedbackScan{}, err
	}
	return row, nil
}
