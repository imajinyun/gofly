package repo

import (
	"database/sql"

	"github.com/imajinyun/gofly/core/storage"
)

type TenantModel interface {
	tenantModel
	withSession(session *sql.Tx) TenantModel
}

func NewTenantModel(conn *storage.SQLStore, dialect ...storage.Dialect) TenantModel {
	if len(dialect) == 0 {
		dialect = []storage.Dialect{storage.DialectQuestion}
	}
	return newTenantModel(conn, dialect...)
}
