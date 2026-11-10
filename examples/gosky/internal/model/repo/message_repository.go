package repo

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
)

var _ app.MessageStore = (*MessageRepository)(nil)

// MessageRepository persists in-app messages. Every read/write is scoped
// by the canonical subject resolved from the verified JWT; receiver ids
// from the browser are converted to subjects through active memberships.
type MessageRepository struct {
	store *storage.SQLStore
}

func NewMessageRepository(store *storage.SQLStore) *MessageRepository {
	return &MessageRepository{store: store}
}

// messageSelectColumns pairs support_messages (alias m) with the receiver's
// browser user id, resolved back through the active users catalog. A deleted
// receiver yields NULL -> 0 rather than dropping the row.
const messageSelectColumns = `m.message_id, m.sender_subject, m.receiver_subject, u.user_id, m.title, m.content,
m.message_type, m.read_flag, m.read_time, m.created_at`

func scanMessage(scanner interface{ Scan(dest ...any) error }) (app.Message, error) {
	var message app.Message
	var read bool
	var readTime sql.NullTime
	var receiverID sql.NullInt64
	if err := scanner.Scan(&message.ID, &message.Sender, &message.Receiver, &receiverID, &message.Title,
		&message.Content, &message.Type, &read, &readTime, &message.CreatedAt); err != nil {
		return app.Message{}, err
	}
	if receiverID.Valid && receiverID.Int64 > 0 {
		message.ReceiverUserID = uint64(receiverID.Int64)
	}
	message.ReceiverUserType = app.MessageReceiverEmployee
	message.ReadFlag = read
	if readTime.Valid {
		t := readTime.Time
		message.ReadTime = &t
	}
	return message, nil
}

func (r *MessageRepository) Send(ctx context.Context, sender string, receiverUserIDs []uint64, input app.MessageInput) (int, error) {
	subjects, err := r.resolveSubjects(ctx, receiverUserIDs)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	sent := 0
	for _, receiver := range subjects {
		result, execErr := r.store.Exec(ctx,
			`INSERT INTO support_messages (sender_subject, receiver_subject, title, content, message_type, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
			sender, receiver, input.Title, input.Content, input.MessageType, now, now)
		if execErr != nil {
			return sent, execErr
		}
		if rows, _ := result.RowsAffected(); rows > 0 {
			sent++
		}
	}
	return sent, nil
}

// resolveSubjects maps active same-tenant user ids to canonical subjects.
func (r *MessageRepository) resolveSubjects(ctx context.Context, userIDs []uint64) ([]string, error) {
	query := `SELECT DISTINCT u.subject
FROM users AS u
JOIN tenant_users AS tu ON tu.user_id = u.user_id
JOIN tenants AS t ON t.tenant_id = tu.tenant_id
WHERE u.user_id IN (` + placeholdersFor(userIDs) + `)
  AND u.user_state = 'active' AND tu.member_state = 'active' AND t.tenant_state = 'active'
ORDER BY u.subject ASC`
	rows, err := r.store.QueryRows(ctx, query, toAnyArgs(userIDs)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	subjects := make([]string, 0, len(userIDs))
	for rows.Next() {
		var subject string
		if err := rows.Scan(&subject); err != nil {
			return nil, err
		}
		subjects = append(subjects, subject)
	}
	return subjects, rows.Err()
}

func (r *MessageRepository) ListInbox(ctx context.Context, subject string, query app.MessageQuery) (app.MessagePage, error) {
	where := []string{"m.receiver_subject = ?"}
	args := []any{subject}
	appendMessageFilters(&where, &args, query)
	return r.queryMessages(ctx, where, args, query.PageNum, query.PageSize)
}

func (r *MessageRepository) CountUnread(ctx context.Context, subject string) (int, error) {
	rows, err := r.store.QueryRows(ctx,
		`SELECT COUNT(*) FROM support_messages WHERE receiver_subject = ? AND read_flag = 0`, subject)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	var count int
	if rows.Next() {
		if err := rows.Scan(&count); err != nil {
			return 0, err
		}
	}
	return count, nil
}

func (r *MessageRepository) MarkRead(ctx context.Context, subject string, id uint64) error {
	result, err := r.store.Exec(ctx,
		`UPDATE support_messages SET read_flag = 1, read_time = ?
WHERE message_id = ? AND receiver_subject = ? AND read_flag = 0`,
		time.Now().UTC(), id, subject)
	if err != nil {
		return err
	}
	return requireRows(result)
}

func (r *MessageRepository) ListAdmin(ctx context.Context, query app.MessageQuery) (app.MessagePage, error) {
	where := make([]string, 0, 4)
	args := make([]any, 0, 4)
	appendMessageFilters(&where, &args, query)
	return r.queryMessages(ctx, where, args, query.PageNum, query.PageSize)
}

func (r *MessageRepository) Delete(ctx context.Context, id uint64) error {
	result, err := r.store.Exec(ctx,
		`DELETE FROM support_messages WHERE message_id = ?`, id)
	if err != nil {
		return err
	}
	return requireRows(result)
}

func appendMessageFilters(where *[]string, args *[]any, query app.MessageQuery) {
	if value := strings.TrimSpace(query.SearchWord); value != "" {
		like := "%" + value + "%"
		*where = append(*where, "(m.title LIKE ? OR m.content LIKE ?)")
		*args = append(*args, like, like)
	}
	if query.ReadFlag != nil {
		*where = append(*where, "m.read_flag = ?")
		*args = append(*args, *query.ReadFlag)
	}
	if query.MessageType != nil {
		*where = append(*where, "m.message_type = ?")
		*args = append(*args, *query.MessageType)
	}
	if value := strings.TrimSpace(query.StartDate); value != "" {
		*where = append(*where, "m.created_at >= ?")
		*args = append(*args, value+" 00:00:00")
	}
	if value := strings.TrimSpace(query.EndDate); value != "" {
		*where = append(*where, "m.created_at <= ?")
		*args = append(*args, value+" 23:59:59")
	}
}

func (r *MessageRepository) queryMessages(ctx context.Context, where []string, args []any, pageNum, pageSize uint64) (app.MessagePage, error) {
	filter := strings.Join(where, " AND ")
	from := ` FROM support_messages AS m
LEFT JOIN users AS u ON u.subject = m.receiver_subject`
	whereClause := ""
	if filter != "" {
		whereClause = " WHERE " + filter
	}

	var total int
	countRows, err := r.store.QueryRows(ctx, `SELECT COUNT(m.message_id)`+from+whereClause, args...)
	if err != nil {
		return app.MessagePage{}, err
	}
	if countRows.Next() {
		if err := countRows.Scan(&total); err != nil {
			_ = countRows.Close()
			return app.MessagePage{}, err
		}
	}
	_ = countRows.Close()

	queryArgs := append(append([]any{}, args...), pageSize, (pageNum-1)*pageSize)
	rows, err := r.store.QueryRows(ctx,
		`SELECT `+messageSelectColumns+from+whereClause+
			` ORDER BY m.created_at DESC, m.message_id DESC LIMIT ? OFFSET ?`,
		queryArgs...)
	if err != nil {
		return app.MessagePage{}, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]app.Message, 0, pageSize)
	for rows.Next() {
		message, scanErr := scanMessage(rows)
		if scanErr != nil {
			return app.MessagePage{}, scanErr
		}
		items = append(items, message)
	}
	return app.MessagePage{List: items, Total: total}, rows.Err()
}
