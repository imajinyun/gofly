package repo

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/imajinyun/gofly/examples/gosky/internal/app"
)

var (
	errDuplicateDictCode  = errors.New("duplicate dict code")
	errDuplicateDictValue = errors.New("duplicate dict value")
	errDuplicateConfigKey = errors.New("duplicate config key")
)

func placeholdersFor(ids []uint64) string {
	parts := make([]string, len(ids))
	for i := range ids {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}

func toAnyArgs(ids []uint64) []any {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return args
}

func requireRows(result sql.Result) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// mapDictError translates MySQL duplicate-key and no-row errors into
// dictionary domain sentinels; other errors pass through unchanged.
func mapDictError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return app.ErrDictNotFound
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "duplicate entry") {
		if strings.Contains(message, "uk_support_dict_data_value") {
			return errDuplicateDictValue
		}
		return errDuplicateDictCode
	}
	return err
}
