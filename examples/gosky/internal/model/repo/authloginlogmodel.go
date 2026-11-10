package repo

import (
	"database/sql"

	"github.com/imajinyun/gofly/core/storage"
)

type AuthLoginLogModel interface {
	authLoginLogModel
	withSession(session *sql.Tx) AuthLoginLogModel
}

func NewAuthLoginLogModel(conn *storage.SQLStore, dialect ...storage.Dialect) AuthLoginLogModel {
	if len(dialect) == 0 {
		dialect = []storage.Dialect{storage.DialectQuestion}
	}
	return newAuthLoginLogModel(conn, dialect...)
}
