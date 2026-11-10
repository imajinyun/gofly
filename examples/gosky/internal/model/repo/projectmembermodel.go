package repo

import (
	"database/sql"

	"github.com/imajinyun/gofly/core/storage"
)

type ProjectMemberModel interface {
	projectMemberModel
	withSession(session *sql.Tx) ProjectMemberModel
}

func NewProjectMemberModel(conn *storage.SQLStore, dialect ...storage.Dialect) ProjectMemberModel {
	if len(dialect) == 0 {
		dialect = []storage.Dialect{storage.DialectQuestion}
	}
	return newProjectMemberModel(conn, dialect...)
}
