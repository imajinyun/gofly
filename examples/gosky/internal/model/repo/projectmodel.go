package repo

import (
	"database/sql"

	"github.com/imajinyun/gofly/core/storage"
)

type ProjectModel interface {
	projectModel
	withSession(session *sql.Tx) ProjectModel
}

func NewProjectModel(conn *storage.SQLStore, dialect ...storage.Dialect) ProjectModel {
	if len(dialect) == 0 {
		dialect = []storage.Dialect{storage.DialectQuestion}
	}
	return newProjectModel(conn, dialect...)
}
