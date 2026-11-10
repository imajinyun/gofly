package repo

import (
	"database/sql"

	"github.com/imajinyun/gofly/core/storage"
)

type AuthSessionModel interface {
	authSessionModel
	withSession(session *sql.Tx) AuthSessionModel
}

func NewAuthSessionModel(conn *storage.SQLStore, dialect ...storage.Dialect) AuthSessionModel {
	if len(dialect) == 0 {
		dialect = []storage.Dialect{storage.DialectQuestion}
	}
	return newAuthSessionModel(conn, dialect...)
}
