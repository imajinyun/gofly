package repo

import (
	"database/sql"

	"github.com/imajinyun/gofly/core/storage"
)

type TenantUserModel interface {
	tenantUserModel
	withSession(session *sql.Tx) TenantUserModel
}

func NewTenantUserModel(conn *storage.SQLStore, dialect ...storage.Dialect) TenantUserModel {
	if len(dialect) == 0 {
		dialect = []storage.Dialect{storage.DialectQuestion}
	}
	return newTenantUserModel(conn, dialect...)
}
