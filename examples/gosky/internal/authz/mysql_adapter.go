package authz

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/casbin/casbin/v2/model"
	"github.com/casbin/casbin/v2/persist"
)

// ErrDirectCasbinMutation prevents callers from bypassing the audited
// MySQLPolicyStore through Enforcer AddPolicy/RemovePolicy methods.
var ErrDirectCasbinMutation = errors.New("use MySQLPolicyStore for Casbin policy mutations")

// MySQLAdapter provides the read side of the Casbin persistence contract.
// Write methods intentionally reject direct Enforcer auto-save calls; those
// lack verified actor data and cannot produce a complete policy audit event.
type MySQLAdapter struct {
	db     *sql.DB
	tables MySQLPolicyTables
}

func NewMySQLAdapter(db *sql.DB) *MySQLAdapter {
	return &MySQLAdapter{db: db, tables: DefaultMySQLPolicyTables()}
}

func NewMySQLAdapterWithTables(db *sql.DB, tables MySQLPolicyTables) (*MySQLAdapter, error) {
	validated, err := NewMySQLPolicyTables(tables.Rules, tables.Events)
	if err != nil {
		return nil, err
	}
	return &MySQLAdapter{db: db, tables: validated}, nil
}

func (a *MySQLAdapter) LoadPolicy(casbinModel model.Model) error {
	if a == nil || a.db == nil {
		return errors.New("casbin policy database is nil")
	}
	rows, err := a.db.QueryContext(context.Background(), a.tables.query(`SELECT ptype, v0, v1, v2, v3, v4, v5 FROM {{rules}} ORDER BY rule_id`))
	if err != nil {
		return fmt.Errorf("load Casbin rules: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var rule policyRule
		if err := rows.Scan(&rule.PType, &rule.Fields[0], &rule.Fields[1], &rule.Fields[2], &rule.Fields[3], &rule.Fields[4], &rule.Fields[5]); err != nil {
			return fmt.Errorf("scan Casbin rule: %w", err)
		}
		if err := persist.LoadPolicyArray(rule.values(), casbinModel); err != nil {
			return fmt.Errorf("apply Casbin rule: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate Casbin rules: %w", err)
	}
	return nil
}

func (*MySQLAdapter) SavePolicy(model.Model) error { return ErrDirectCasbinMutation }

func (*MySQLAdapter) AddPolicy(string, string, []string) error { return ErrDirectCasbinMutation }

func (*MySQLAdapter) RemovePolicy(string, string, []string) error { return ErrDirectCasbinMutation }

func (*MySQLAdapter) RemoveFilteredPolicy(string, string, int, ...string) error {
	return ErrDirectCasbinMutation
}

var _ persist.Adapter = (*MySQLAdapter)(nil)
