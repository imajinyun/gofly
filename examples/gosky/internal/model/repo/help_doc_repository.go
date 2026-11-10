package repo

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
)

var _ app.HelpDocStore = (*HelpDocRepository)(nil)

// HelpDocRepository persists the tenant help-document knowledge base. All
// values are SQL parameters and every statement is tenant-scoped; browser
// input never reaches the catalog tree or relation ids unchecked.
type HelpDocRepository struct {
	store *storage.SQLStore
}

func NewHelpDocRepository(store *storage.SQLStore) *HelpDocRepository {
	return &HelpDocRepository{store: store}
}

const helpDocCatalogColumns = `help_doc_catalog_id, name, parent_id, sort`

const helpDocColumns = `d.help_doc_id, d.help_doc_catalog_id, COALESCE(c.name, ''), d.title,
d.author, d.sort, d.page_view_count, d.user_view_count, d.created_at, d.updated_at`

// ---------------- catalogs ----------------

func (r *HelpDocRepository) ListCatalogs(ctx context.Context, tenantID uint64) ([]app.HelpDocCatalog, error) {
	rows, err := r.store.QueryRows(ctx,
		`SELECT `+helpDocCatalogColumns+` FROM support_help_doc_catalogs
WHERE tenant_id = ? ORDER BY sort ASC, help_doc_catalog_id ASC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	catalogs := make([]app.HelpDocCatalog, 0)
	for rows.Next() {
		var catalog app.HelpDocCatalog
		if err := rows.Scan(&catalog.ID, &catalog.Name, &catalog.ParentID, &catalog.Sort); err != nil {
			return nil, err
		}
		catalogs = append(catalogs, catalog)
	}
	return catalogs, rows.Err()
}

func (r *HelpDocRepository) CreateCatalog(ctx context.Context, tenantID uint64, input app.HelpDocCatalogInput) error {
	if input.ParentID != 0 {
		if err := r.requireCatalog(ctx, tenantID, input.ParentID); err != nil {
			return err
		}
	}
	result, err := r.store.Exec(ctx,
		`INSERT INTO support_help_doc_catalogs (tenant_id, name, parent_id, sort) VALUES (?, ?, ?, ?)`,
		tenantID, input.Name, input.ParentID, input.Sort)
	if err != nil {
		if isDuplicateHelpDocKey(err) {
			return app.ErrHelpDocCatalogDuplicateName
		}
		return err
	}
	return requireRows(result)
}

func (r *HelpDocRepository) UpdateCatalog(ctx context.Context, tenantID uint64, input app.HelpDocCatalogInput) error {
	if input.ParentID != 0 {
		if input.ParentID == input.ID {
			return app.ErrHelpDocCatalogCycle
		}
		if err := r.requireCatalog(ctx, tenantID, input.ParentID); err != nil {
			return err
		}
		if err := r.rejectDescendantParent(ctx, tenantID, input.ID, input.ParentID); err != nil {
			return err
		}
	}
	result, err := r.store.Exec(ctx,
		`UPDATE support_help_doc_catalogs SET name = ?, parent_id = ?, sort = ?
WHERE tenant_id = ? AND help_doc_catalog_id = ?`,
		input.Name, input.ParentID, input.Sort, tenantID, input.ID)
	if err != nil {
		if isDuplicateHelpDocKey(err) {
			return app.ErrHelpDocCatalogDuplicateName
		}
		return err
	}
	return requireRows(result)
}

func (r *HelpDocRepository) DeleteCatalog(ctx context.Context, tenantID, id uint64) error {
	if err := r.requireCatalog(ctx, tenantID, id); err != nil {
		return err
	}
	var childCount int
	if err := r.store.QueryRow(ctx,
		`SELECT COUNT(*) FROM support_help_doc_catalogs WHERE tenant_id = ? AND parent_id = ?`,
		tenantID, id).Scan(&childCount); err != nil {
		return err
	}
	if childCount > 0 {
		return app.ErrHelpDocCatalogHasChildren
	}
	var docCount int
	if err := r.store.QueryRow(ctx,
		`SELECT COUNT(*) FROM support_help_docs WHERE tenant_id = ? AND help_doc_catalog_id = ?`,
		tenantID, id).Scan(&docCount); err != nil {
		return err
	}
	if docCount > 0 {
		return app.ErrHelpDocCatalogInUse
	}
	result, err := r.store.Exec(ctx,
		`DELETE FROM support_help_doc_catalogs WHERE tenant_id = ? AND help_doc_catalog_id = ?`, tenantID, id)
	if err != nil {
		return err
	}
	return requireRows(result)
}

func (r *HelpDocRepository) requireCatalog(ctx context.Context, tenantID, id uint64) error {
	var found uint64
	err := r.store.QueryRow(ctx,
		`SELECT help_doc_catalog_id FROM support_help_doc_catalogs WHERE tenant_id = ? AND help_doc_catalog_id = ?`,
		tenantID, id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return app.ErrHelpDocCatalogNotFound
	}
	return err
}

// rejectDescendantParent walks the catalog chain to ensure newParent is not
// the node itself or one of its descendants.
func (r *HelpDocRepository) rejectDescendantParent(ctx context.Context, tenantID, nodeID, newParent uint64) error {
	current := newParent
	for range 64 {
		if current == nodeID {
			return app.ErrHelpDocCatalogCycle
		}
		var parentID uint64
		err := r.store.QueryRow(ctx,
			`SELECT parent_id FROM support_help_doc_catalogs WHERE tenant_id = ? AND help_doc_catalog_id = ?`,
			tenantID, current).Scan(&parentID)
		if errors.Is(err, sql.ErrNoRows) {
			return app.ErrHelpDocCatalogNotFound
		}
		if err != nil {
			return err
		}
		if parentID == 0 {
			return nil
		}
		current = parentID
	}
	return app.ErrHelpDocCatalogCycle
}

// ---------------- documents ----------------

func (r *HelpDocRepository) ListDocs(ctx context.Context, tenantID uint64, query app.HelpDocQuery) (app.HelpDocPage, error) {
	where := []string{"d.tenant_id = ?"}
	args := []any{tenantID}
	if query.CatalogID != nil {
		where = append(where, "d.help_doc_catalog_id = ?")
		args = append(args, *query.CatalogID)
	}
	if value := strings.TrimSpace(query.Keywords); value != "" {
		like := "%" + value + "%"
		where = append(where, "(d.title LIKE ? OR d.author LIKE ?)")
		args = append(args, like, like)
	}
	if value := strings.TrimSpace(query.CreateTimeBegin); value != "" {
		where = append(where, "d.created_at >= ?")
		args = append(args, value+" 00:00:00")
	}
	if value := strings.TrimSpace(query.CreateTimeEnd); value != "" {
		where = append(where, "d.created_at <= ?")
		args = append(args, value+" 23:59:59")
	}
	filter := strings.Join(where, " AND ")

	var total int
	if err := r.store.QueryRow(ctx,
		`SELECT COUNT(*) FROM support_help_docs AS d WHERE `+filter, args...).Scan(&total); err != nil {
		return app.HelpDocPage{}, err
	}

	pageArgs := append(append([]any{}, args...), query.PageSize, (query.PageNum-1)*query.PageSize)
	rows, err := r.store.QueryRows(ctx,
		`SELECT `+helpDocColumns+` FROM support_help_docs AS d
LEFT JOIN support_help_doc_catalogs AS c ON c.tenant_id = d.tenant_id AND c.help_doc_catalog_id = d.help_doc_catalog_id
WHERE `+filter+` ORDER BY d.sort ASC, d.created_at DESC LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return app.HelpDocPage{}, err
	}
	defer func() { _ = rows.Close() }()
	docs, err := scanHelpDocs(rows)
	if err != nil {
		return app.HelpDocPage{}, err
	}
	return app.HelpDocPage{List: docs, Total: total}, nil
}

func (r *HelpDocRepository) ListAllDocs(ctx context.Context, tenantID uint64) ([]app.HelpDoc, error) {
	rows, err := r.store.QueryRows(ctx,
		`SELECT `+helpDocColumns+` FROM support_help_docs AS d
LEFT JOIN support_help_doc_catalogs AS c ON c.tenant_id = d.tenant_id AND c.help_doc_catalog_id = d.help_doc_catalog_id
WHERE d.tenant_id = ? ORDER BY d.sort ASC, d.created_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanHelpDocs(rows)
}

func (r *HelpDocRepository) ListDocsByRelation(ctx context.Context, tenantID, relationID uint64) ([]app.HelpDoc, error) {
	rows, err := r.store.QueryRows(ctx,
		`SELECT `+helpDocColumns+` FROM support_help_doc_relations AS r
JOIN support_help_docs AS d ON d.tenant_id = r.tenant_id AND d.help_doc_id = r.help_doc_id
LEFT JOIN support_help_doc_catalogs AS c ON c.tenant_id = d.tenant_id AND c.help_doc_catalog_id = d.help_doc_catalog_id
WHERE r.tenant_id = ? AND r.relation_id = ? ORDER BY d.sort ASC, d.created_at DESC`,
		tenantID, relationID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanHelpDocs(rows)
}

func scanHelpDocs(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}) ([]app.HelpDoc, error) {
	docs := make([]app.HelpDoc, 0)
	for rows.Next() {
		var doc app.HelpDoc
		if err := rows.Scan(&doc.ID, &doc.CatalogID, &doc.CatalogName, &doc.Title,
			&doc.Author, &doc.Sort, &doc.PageViewCount, &doc.UserViewCount, &doc.CreatedAt, &doc.UpdatedAt); err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}

func (r *HelpDocRepository) DocDetail(ctx context.Context, tenantID, id uint64, includeRelations bool) (app.HelpDocDetail, error) {
	var detail app.HelpDocDetail
	var attachmentKeys string
	err := r.store.QueryRow(ctx,
		`SELECT d.help_doc_id, d.help_doc_catalog_id, d.title, d.content_text, d.content_html,
d.attachment, d.author, d.sort, d.page_view_count, d.user_view_count, d.created_at, d.updated_at
FROM support_help_docs AS d WHERE d.tenant_id = ? AND d.help_doc_id = ?`, tenantID, id).
		Scan(&detail.ID, &detail.CatalogID, &detail.Title, &detail.ContentText, &detail.ContentHTML,
			&attachmentKeys, &detail.Author, &detail.Sort, &detail.PageViewCount, &detail.UserViewCount,
			&detail.CreatedAt, &detail.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return app.HelpDocDetail{}, app.ErrHelpDocNotFound
	}
	if err != nil {
		return app.HelpDocDetail{}, err
	}
	keys := splitAttachmentKeys(attachmentKeys)
	filesByKey, err := loadFileMetadataByKeys(ctx, r.store, tenantID, keys)
	if err != nil {
		return app.HelpDocDetail{}, err
	}
	detail.Attachments = resolveInKeyOrder(keys, filesByKey)
	if includeRelations {
		relations, err := r.listRelations(ctx, tenantID, id)
		if err != nil {
			return app.HelpDocDetail{}, err
		}
		detail.Relations = relations
	}
	return detail, nil
}

func (r *HelpDocRepository) listRelations(ctx context.Context, tenantID, docID uint64) ([]app.HelpDocRelation, error) {
	rows, err := r.store.QueryRows(ctx,
		`SELECT relation_name, relation_id FROM support_help_doc_relations
WHERE tenant_id = ? AND help_doc_id = ? ORDER BY id ASC`, tenantID, docID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	relations := make([]app.HelpDocRelation, 0)
	for rows.Next() {
		var relation app.HelpDocRelation
		if err := rows.Scan(&relation.RelationName, &relation.RelationID); err != nil {
			return nil, err
		}
		relations = append(relations, relation)
	}
	return relations, rows.Err()
}

func (r *HelpDocRepository) CreateDoc(ctx context.Context, tenantID uint64, input app.HelpDocInput) (uint64, error) {
	if err := r.requireCatalog(ctx, tenantID, input.CatalogID); err != nil {
		return 0, err
	}
	var newID uint64
	err := r.store.Transact(ctx, nil, func(txCtx context.Context, tx *sql.Tx) error {
		result, execErr := tx.ExecContext(txCtx,
			`INSERT INTO support_help_docs
(tenant_id, help_doc_catalog_id, title, content_text, content_html, attachment, sort, author)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			tenantID, input.CatalogID, input.Title, input.ContentText, input.ContentHTML,
			strings.Join(input.AttachmentKeys, ","), input.Sort, input.Author)
		if execErr != nil {
			return execErr
		}
		inserted, execErr := result.LastInsertId()
		if execErr != nil {
			return execErr
		}
		newID = uint64(inserted)
		return insertHelpDocRelations(txCtx, tx, tenantID, newID, input.Relations)
	})
	if err != nil {
		return 0, err
	}
	return newID, nil
}

func (r *HelpDocRepository) UpdateDoc(ctx context.Context, tenantID uint64, input app.HelpDocInput) error {
	if err := r.requireCatalog(ctx, tenantID, input.CatalogID); err != nil {
		return err
	}
	return r.store.Transact(ctx, nil, func(txCtx context.Context, tx *sql.Tx) error {
		result, execErr := tx.ExecContext(txCtx,
			`UPDATE support_help_docs SET help_doc_catalog_id = ?, title = ?, content_text = ?,
content_html = ?, attachment = ?, sort = ?, author = ? WHERE tenant_id = ? AND help_doc_id = ?`,
			input.CatalogID, input.Title, input.ContentText, input.ContentHTML,
			strings.Join(input.AttachmentKeys, ","), input.Sort, input.Author, tenantID, input.ID)
		if execErr != nil {
			return execErr
		}
		if rows, _ := result.RowsAffected(); rows == 0 {
			return app.ErrHelpDocNotFound
		}
		if _, execErr := tx.ExecContext(txCtx,
			`DELETE FROM support_help_doc_relations WHERE tenant_id = ? AND help_doc_id = ?`,
			tenantID, input.ID); execErr != nil {
			return execErr
		}
		return insertHelpDocRelations(txCtx, tx, tenantID, input.ID, input.Relations)
	})
}

func insertHelpDocRelations(ctx context.Context, tx *sql.Tx, tenantID, docID uint64, relations []app.HelpDocRelation) error {
	for _, relation := range relations {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO support_help_doc_relations (tenant_id, help_doc_id, relation_id, relation_name)
VALUES (?, ?, ?, ?)`, tenantID, docID, relation.RelationID, relation.RelationName); err != nil {
			return err
		}
	}
	return nil
}

func (r *HelpDocRepository) DeleteDoc(ctx context.Context, tenantID, id uint64) error {
	return r.store.Transact(ctx, nil, func(txCtx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(txCtx,
			`DELETE FROM support_help_docs WHERE tenant_id = ? AND help_doc_id = ?`, tenantID, id)
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows == 0 {
			return app.ErrHelpDocNotFound
		}
		if _, err := tx.ExecContext(txCtx,
			`DELETE FROM support_help_doc_relations WHERE tenant_id = ? AND help_doc_id = ?`, tenantID, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(txCtx,
			`DELETE FROM support_help_doc_view_records WHERE tenant_id = ? AND help_doc_id = ?`, tenantID, id); err != nil {
			return err
		}
		return nil
	})
}

// ---------------- view records ----------------

// RecordView resolves the subject to an active same-tenant user and upserts
// the per-user view record in one transaction with the document counters.
// newUser is true only when the view row was inserted for the first time.
func (r *HelpDocRepository) RecordView(ctx context.Context, tenantID, docID uint64, subject, ip, userAgent string) (bool, error) {
	newUser := false
	err := r.store.Transact(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead}, func(txCtx context.Context, tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(txCtx,
			`SELECT 1 FROM support_help_docs WHERE tenant_id = ? AND help_doc_id = ? FOR UPDATE`,
			tenantID, docID).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return app.ErrHelpDocNotFound
			}
			return err
		}
		var userID uint64
		var userName string
		err := tx.QueryRowContext(txCtx,
			`SELECT u.user_id, COALESCE(NULLIF(tu.display_name, ''), u.display_name)
FROM users AS u
JOIN tenant_users AS tu ON tu.user_id = u.user_id
WHERE tu.tenant_id = ? AND u.subject = ?
  AND u.user_state = 'active' AND tu.member_state = 'active'`,
			tenantID, subject).Scan(&userID, &userName)
		if errors.Is(err, sql.ErrNoRows) {
			return app.ErrHelpDocNotFound
		}
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(txCtx,
			`INSERT INTO support_help_doc_view_records
(tenant_id, help_doc_id, user_id, user_name, first_ip, first_user_agent, last_ip, last_user_agent, page_view_count)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1)
ON DUPLICATE KEY UPDATE page_view_count = page_view_count + 1,
last_ip = VALUES(last_ip), last_user_agent = VALUES(last_user_agent)`,
			tenantID, docID, userID, userName, ip, userAgent, ip, userAgent)
		if err != nil {
			return err
		}
		affected, _ := result.RowsAffected()
		newUser = affected == 1
		pageIncrement := 1
		userIncrement := 0
		if newUser {
			userIncrement = 1
		}
		_, err = tx.ExecContext(txCtx,
			`UPDATE support_help_docs SET page_view_count = page_view_count + ?,
user_view_count = user_view_count + ? WHERE tenant_id = ? AND help_doc_id = ?`,
			pageIncrement, userIncrement, tenantID, docID)
		return err
	})
	if err != nil {
		return false, err
	}
	return newUser, nil
}

func (r *HelpDocRepository) ListViewRecords(ctx context.Context, tenantID uint64, query app.HelpDocViewRecordQuery) (app.HelpDocViewRecordPage, error) {
	where := []string{"tenant_id = ?", "help_doc_id = ?"}
	args := []any{tenantID, query.HelpDocID}
	if query.UserID != nil {
		where = append(where, "user_id = ?")
		args = append(args, *query.UserID)
	}
	if value := strings.TrimSpace(query.Keywords); value != "" {
		like := "%" + value + "%"
		where = append(where, "(user_name LIKE ? OR first_ip LIKE ? OR first_user_agent LIKE ? OR last_ip LIKE ? OR last_user_agent LIKE ?)")
		for range 5 {
			args = append(args, like)
		}
	}
	filter := strings.Join(where, " AND ")

	var total int
	countRows, err := r.store.QueryRows(ctx,
		`SELECT COUNT(*) FROM support_help_doc_view_records WHERE `+filter, args...)
	if err != nil {
		return app.HelpDocViewRecordPage{}, err
	}
	if countRows.Next() {
		if err := countRows.Scan(&total); err != nil {
			_ = countRows.Close()
			return app.HelpDocViewRecordPage{}, err
		}
	}
	_ = countRows.Close()

	pageArgs := append(append([]any{}, args...), query.PageSize, (query.PageNum-1)*query.PageSize)
	rows, err := r.store.QueryRows(ctx,
		`SELECT user_id, user_name, page_view_count, first_ip, first_user_agent, last_ip, last_user_agent,
created_at, updated_at FROM support_help_doc_view_records WHERE `+filter+
			` ORDER BY updated_at DESC, created_at DESC LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return app.HelpDocViewRecordPage{}, err
	}
	defer func() { _ = rows.Close() }()
	records := make([]app.HelpDocViewRecord, 0, query.PageSize)
	for rows.Next() {
		var record app.HelpDocViewRecord
		if err := rows.Scan(&record.UserID, &record.UserName, &record.PageViewCount,
			&record.FirstIP, &record.FirstUserAgent, &record.LastIP, &record.LastUserAgent,
			&record.CreatedAt, &record.UpdatedAt); err != nil {
			return app.HelpDocViewRecordPage{}, err
		}
		records = append(records, record)
	}
	return app.HelpDocViewRecordPage{List: records, Total: total}, rows.Err()
}

func isDuplicateHelpDocKey(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
