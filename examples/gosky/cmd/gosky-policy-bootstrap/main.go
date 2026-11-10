// gosky-policy-bootstrap initializes one tenant's immutable base policy and
// first administrator. It is deliberately separate from the HTTP service so a
// request cannot create a tenant's initial administrative authority.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
	"github.com/imajinyun/gofly/examples/gosky/internal/model/repo"

	_ "github.com/go-sql-driver/mysql"
)

func main() {
	dsn := strings.TrimSpace(os.Getenv("GOSKY_MYSQL_DSN"))
	tenant := strings.TrimSpace(os.Getenv("GOSKY_POLICY_BOOTSTRAP_TENANT"))
	admin := strings.TrimSpace(os.Getenv("GOSKY_POLICY_BOOTSTRAP_ADMIN"))
	actor := strings.TrimSpace(os.Getenv("GOSKY_POLICY_BOOTSTRAP_ACTOR"))
	upgradeExplicit := strings.EqualFold(strings.TrimSpace(os.Getenv("GOSKY_POLICY_UPGRADE_EXPLICIT")), "true")
	upgradeCapabilities := strings.EqualFold(strings.TrimSpace(os.Getenv("GOSKY_POLICY_UPGRADE_CAPABILITIES")), "true")
	if dsn == "" || tenant == "" || actor == "" {
		slog.Error("policy operator configuration is incomplete", "required", "GOSKY_MYSQL_DSN,GOSKY_POLICY_BOOTSTRAP_TENANT,GOSKY_POLICY_BOOTSTRAP_ACTOR")
		return
	}
	if !upgradeCapabilities && !upgradeExplicit && admin == "" {
		slog.Error("bootstrap configuration is incomplete", "required", "GOSKY_POLICY_BOOTSTRAP_ADMIN")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		slog.Error("open policy database", "error", err)
		return
	}
	defer func() { _ = db.Close() }()
	if err := db.PingContext(ctx); err != nil {
		slog.Error("ping policy database", "error", err)
		return
	}
	tables, err := authz.NewMySQLPolicyTables(os.Getenv("GOSKY_POLICY_RULES_TABLE"), os.Getenv("GOSKY_POLICY_EVENTS_TABLE"))
	if err != nil {
		slog.Error("resolve policy table names", "error", err)
		return
	}
	store, err := authz.NewMySQLPolicyStoreWithTables(db, tables)
	if err != nil {
		slog.Error("create policy store", "error", err)
		return
	}
	// Provision the reserved roles, the deployment-owned menu catalog, and
	// the root organization department for numeric identity tenants. String
	// demo domains have no identity rows.
	sqlStore := storage.NewSQLStore(db)
	rbacRepository := repo.NewMySQLRBACRepository(sqlStore, store)
	store.SetRoleDirectory(rbacRepository)
	orgRepository := repo.NewOrgRepository(sqlStore, rbacRepository)
	if tenantID, parseErr := strconv.ParseUint(tenant, 10, 64); parseErr == nil && tenantID > 0 {
		if err := rbacRepository.EnsureTenantCatalog(ctx, tenantID); err != nil {
			slog.Error("seed tenant rbac catalog", "tenant", tenant, "error", err)
			return
		}
		if err := orgRepository.EnsureTenantOrganization(ctx, tenantID); err != nil {
			slog.Error("seed tenant organization", "tenant", tenant, "error", err)
			return
		}
	}
	operator := authz.PolicyActor{Subject: actor, Domain: tenant, RequestID: "operator-bootstrap"}
	if upgradeExplicit {
		changed, err := store.UpgradeTenantExplicitPermissions(ctx, operator)
		if err != nil {
			slog.Error("convert explicit tenant permissions", "tenant", tenant, "error", err)
			return
		}
		fmt.Printf("explicit tenant permissions converted for tenant=%s changed=%t\n", tenant, changed)
		return
	}
	if upgradeCapabilities {
		changed, upgradeErr := store.UpgradeTenantCapabilities(ctx, operator)
		if upgradeErr != nil {
			slog.Error("upgrade tenant capability policies", "tenant", tenant, "error", upgradeErr)
			return
		}
		fmt.Printf("tenant capability policies upgraded for tenant=%s changed=%t\n", tenant, changed)
		return
	}
	if err := store.BootstrapTenant(ctx, operator, admin); err != nil {
		if errors.Is(err, authz.ErrTenantAlreadyBootstrapped) {
			slog.Error("tenant policy already bootstrapped", "tenant", tenant)
			return
		}
		slog.Error("bootstrap tenant policy", "tenant", tenant, "error", err)
		return
	}
	fmt.Printf("tenant policy bootstrapped for tenant=%s admin=%s\n", tenant, admin)
}
