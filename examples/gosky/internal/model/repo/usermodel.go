package repo

import (
	"database/sql"

	"github.com/imajinyun/gofly/core/storage"
)

type UserModel interface {
	userModel
	withSession(session *sql.Tx) UserModel
}

func NewUserModel(conn *storage.SQLStore, dialect ...storage.Dialect) UserModel {
	if len(dialect) == 0 {
		dialect = []storage.Dialect{storage.DialectQuestion}
	}
	return newUserModel(conn, dialect...)
}
