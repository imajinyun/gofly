package repo

import (
	"context"
	"database/sql"
	"time"

	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
)

var _ app.SerialNumberStore = (*SerialNumberRepository)(nil)

// SerialNumberRepository implements the row-locked DB generator. The
// lock/advance/upsert sequence runs in one Repeatable Read transaction so
// concurrent generators never observe the same last number (no Redis).
type SerialNumberRepository struct {
	store *storage.SQLStore
}

func NewSerialNumberRepository(store *storage.SQLStore) *SerialNumberRepository {
	return &SerialNumberRepository{store: store}
}

const serialDefinitionColumns = `serial_number_id, business_name, format, rule_type, init_number,
step_random_range, last_number, last_time, remark, system_flag, created_at, updated_at`

func scanSerialDefinition(scanner interface{ Scan(dest ...any) error }) (app.SerialNumber, error) {
	var definition app.SerialNumber
	var ruleType string
	var lastNumber sql.NullInt64
	var lastTime sql.NullTime
	var system bool
	if err := scanner.Scan(&definition.ID, &definition.BusinessName, &definition.Format, &ruleType,
		&definition.InitNumber, &definition.StepRandomRange, &lastNumber, &lastTime,
		&definition.Remark, &system, &definition.CreatedAt, &definition.UpdatedAt); err != nil {
		return app.SerialNumber{}, err
	}
	definition.RuleType = ruleType
	if lastNumber.Valid {
		value := lastNumber.Int64
		definition.LastNumber = &value
	}
	if lastTime.Valid {
		t := lastTime.Time
		definition.LastTime = &t
	}
	return definition, nil
}

func (r *SerialNumberRepository) ListDefinitions(ctx context.Context) ([]app.SerialNumber, error) {
	rows, err := r.store.QueryRows(ctx,
		`SELECT `+serialDefinitionColumns+` FROM support_serial_numbers ORDER BY serial_number_id ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]app.SerialNumber, 0)
	for rows.Next() {
		definition, scanErr := scanSerialDefinition(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, definition)
	}
	return items, rows.Err()
}

// GenerateInTx runs the whole generation in one transaction: the
// definition row is locked FOR UPDATE, the compute callback advances the
// counter, and both the definition state and the daily summary are updated.
func (r *SerialNumberRepository) GenerateInTx(ctx context.Context, id uint64, count int, now time.Time, compute app.SerialNumberCompute) (app.SerialNumber, []int64, error) {
	var definition app.SerialNumber
	var numbers []int64
	err := r.store.Transact(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead}, func(txCtx context.Context, tx *sql.Tx) error {
		row := tx.QueryRowContext(txCtx,
			`SELECT `+serialDefinitionColumns+` FROM support_serial_numbers WHERE serial_number_id = ? FOR UPDATE`, id)
		locked, err := scanSerialDefinition(row)
		if err != nil {
			return err
		}
		if locked.StepRandomRange < 1 {
			return app.ErrSerialMisconfigured
		}
		generated, last := compute(locked, now, count)
		if _, err := tx.ExecContext(txCtx,
			`UPDATE support_serial_numbers SET last_number = ?, last_time = ? WHERE serial_number_id = ?`,
			last, now, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(txCtx, `INSERT INTO support_serial_number_records
(serial_number_id, record_date, last_number, last_time, generate_count)
VALUES (?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE last_number = VALUES(last_number), last_time = VALUES(last_time),
generate_count = generate_count + VALUES(generate_count), updated_at = CURRENT_TIMESTAMP(6)`,
			id, now.Format("2006-01-02"), last, now, len(generated)); err != nil {
			return err
		}
		definition = locked
		numbers = generated
		return nil
	})
	if err != nil {
		return app.SerialNumber{}, nil, err
	}
	return definition, numbers, nil
}

func (r *SerialNumberRepository) ListRecords(ctx context.Context, id uint64, pageNum, pageSize uint64) (app.SerialNumberRecordPage, error) {
	var total int
	countRows, err := r.store.QueryRows(ctx,
		`SELECT COUNT(*) FROM support_serial_number_records WHERE serial_number_id = ?`, id)
	if err != nil {
		return app.SerialNumberRecordPage{}, err
	}
	if countRows.Next() {
		if err := countRows.Scan(&total); err != nil {
			_ = countRows.Close()
			return app.SerialNumberRecordPage{}, err
		}
	}
	_ = countRows.Close()

	rows, err := r.store.QueryRows(ctx,
		`SELECT id, serial_number_id, DATE_FORMAT(record_date, '%Y-%m-%d'), generate_count, last_number, last_time
FROM support_serial_number_records
WHERE serial_number_id = ?
ORDER BY record_date DESC
LIMIT ? OFFSET ?`,
		id, pageSize, (pageNum-1)*pageSize)
	if err != nil {
		return app.SerialNumberRecordPage{}, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]app.SerialNumberRecord, 0, pageSize)
	for rows.Next() {
		var record app.SerialNumberRecord
		if err := rows.Scan(&record.ID, &record.SerialNumberID, &record.RecordDate,
			&record.Count, &record.LastNumber, &record.LastTime); err != nil {
			return app.SerialNumberRecordPage{}, err
		}
		items = append(items, record)
	}
	return app.SerialNumberRecordPage{List: items, Total: total}, rows.Err()
}
