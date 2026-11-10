package repo

import (
	"database/sql"

	"github.com/imajinyun/gofly/core/storage"
)

type OpslogModel interface {
	opslogModel
	withSession(session *sql.Tx) OpslogModel
}

func NewOpslogModel(conn *storage.SQLStore, dialect ...storage.Dialect) OpslogModel {
	if len(dialect) == 0 {
		dialect = []storage.Dialect{storage.DialectQuestion}
	}
	return newOpslogModel(conn, dialect...)
}
