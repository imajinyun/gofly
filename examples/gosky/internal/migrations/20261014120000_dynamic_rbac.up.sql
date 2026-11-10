-- Dynamic, tenant-managed RBAC: custom roles, tenant menu catalog, role-menu
-- grants, and admin operation audit.
--
-- Design and executable failure contracts:
--   docs/authorization/dynamic-rbac.md
--   docs/authorization/failure-mode-inventory.md (DYNA-*)
--
-- The capability vocabulary stays deployment-owned: menus.capability may only
-- name a value in authz.ManagedCapabilities(), enforced by the application
-- service. Casbin rules and the fixed reader/writer/admin hierarchy are not
-- modified by this migration; it only seeds the presentation layer and adds
-- the role-existence table used to validate custom grouping rules.
--
-- Assumptions: MySQL 8.0+ (REGEXP_LIKE and CHECK constraints), identity and
-- casbin policy migrations already applied.

CREATE TABLE roles (
    role_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Global role row ID',
    tenant_id BIGINT UNSIGNED NOT NULL COMMENT 'Tenant that owns the role',
    role_code VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Tenant-scoped Casbin role code',
    role_name VARCHAR(255) NOT NULL COMMENT 'Role display name',
    remark VARCHAR(512) NOT NULL DEFAULT '' COMMENT 'Role remark',
    role_state ENUM('active', 'disabled') NOT NULL DEFAULT 'active' COMMENT 'Role state[active:available disabled:cannot be assigned]',
    system_flag TINYINT(1) NOT NULL DEFAULT 0 COMMENT 'Reserved reader/writer/admin roles when true',
    created_by VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Creator subject',
    updated_by VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Last updater subject',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT 'Creation time',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT 'Last update time',

    PRIMARY KEY (role_id),
    UNIQUE KEY uk_roles_tenant_code (tenant_id, role_code),
    UNIQUE KEY uk_roles_tenant_id (tenant_id, role_id),
    KEY idx_roles_tenant_state (tenant_id, role_state, role_id),
    CHECK (REGEXP_LIKE(role_code, '^[a-z][a-z0-9_]{1,63}$')),
    CHECK (role_code NOT IN ('reader', 'writer', 'admin') OR system_flag = 1),
    CHECK (system_flag IN (0, 1))
) ENGINE=InnoDB
  DEFAULT CHARSET=utf8mb4
  COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Tenant-managed role definitions';


CREATE TABLE menus (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Internal row ID',
    tenant_id BIGINT UNSIGNED NOT NULL COMMENT 'Tenant that owns the menu row',
    menu_id BIGINT UNSIGNED NOT NULL COMMENT 'External SmartAdmin menu ID; fixed for seeded rows, tenant-allocated above 820000000',
    parent_id BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Parent menu ID; 0 is a root node',
    menu_name VARCHAR(255) NOT NULL COMMENT 'Menu display name',
    menu_type TINYINT UNSIGNED NOT NULL COMMENT 'Menu type[1:catalog 2:page 3:function point]',
    sort_order INT NOT NULL DEFAULT 0 COMMENT 'Display sort order',
    path VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'Browser route path',
    component VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'Compiled browser component key, never a filesystem path or URL',
    web_perms VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'Operator button permission key',
    capability VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'Deployment-owned capability for function points; empty otherwise',
    icon VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'Display icon key',
    visible_flag TINYINT(1) NOT NULL DEFAULT 1 COMMENT 'Whether the menu is visible',
    disabled_flag TINYINT(1) NOT NULL DEFAULT 0 COMMENT 'Whether the menu is disabled',
    system_flag TINYINT(1) NOT NULL DEFAULT 0 COMMENT 'Seeded immutable rows when true',
    created_by VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Creator subject',
    updated_by VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Last updater subject',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT 'Creation time',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT 'Last update time',

    PRIMARY KEY (id),
    UNIQUE KEY uk_menus_tenant_menu (tenant_id, menu_id),
    KEY idx_menus_tenant_parent (tenant_id, parent_id, disabled_flag, sort_order, menu_id),
    KEY idx_menus_tenant_capability (tenant_id, capability),
    CHECK (menu_type IN (1, 2, 3)),
    CHECK (visible_flag IN (0, 1)),
    CHECK (disabled_flag IN (0, 1)),
    CHECK (system_flag IN (0, 1)),
    CHECK (
        system_flag = 1
        OR (
            (menu_type = 3 AND capability <> '')
            OR (menu_type <> 3 AND capability = '')
        )
    ),
    CHECK (
        capability = ''
        OR REGEXP_LIKE(capability, '^[a-z][a-z0-9]*([.][a-z][a-z0-9_]*)+$')
    )
) ENGINE=InnoDB
  DEFAULT CHARSET=utf8mb4
  COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Tenant menu catalog projected into the admin console';


CREATE TABLE role_menus (
    tenant_id BIGINT UNSIGNED NOT NULL COMMENT 'Tenant that owns the grant',
    role_id BIGINT UNSIGNED NOT NULL COMMENT 'Granted role row ID',
    menu_id BIGINT UNSIGNED NOT NULL COMMENT 'Granted menu point ID',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT 'Grant time',

    PRIMARY KEY (tenant_id, role_id, menu_id),
    KEY idx_role_menus_menu (tenant_id, menu_id),
    CONSTRAINT fk_role_menus_role
        FOREIGN KEY (tenant_id, role_id)
        REFERENCES roles (tenant_id, role_id)
        ON DELETE CASCADE,
    CONSTRAINT fk_role_menus_menu
        FOREIGN KEY (tenant_id, menu_id)
        REFERENCES menus (tenant_id, menu_id)
        ON DELETE CASCADE
) ENGINE=InnoDB
  DEFAULT CHARSET=utf8mb4
  COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Role-to-menu-point grants used to reconcile Casbin p rules';


CREATE TABLE admin_audit_logs (
    log_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Admin audit log ID',
    tenant_id BIGINT UNSIGNED NOT NULL COMMENT 'Actor tenant ID',
    actor_subject VARCHAR(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'Actor subject',
    operation VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Admin operation type',
    object_type VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Target object type',
    object_id VARCHAR(64) NOT NULL DEFAULT '' COMMENT 'Target object identifier',
    before_state JSON NULL COMMENT 'Object state before mutation; null for creates',
    after_state JSON NULL COMMENT 'Object state after mutation; null for deletes',
    request_id VARCHAR(128) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Request correlation ID',
    trace_id VARCHAR(128) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Trace correlation ID',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT 'Audit time',

    PRIMARY KEY (log_id),
    KEY idx_admin_audit_tenant_created (tenant_id, created_at DESC, log_id),
    KEY idx_admin_audit_actor_created (tenant_id, actor_subject, created_at DESC, log_id),
    KEY idx_admin_audit_object (tenant_id, object_type, object_id, created_at DESC),
    CHECK (operation <> ''),
    CHECK (object_type <> '')
) ENGINE=InnoDB
  DEFAULT CHARSET=utf8mb4
  COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Admin console mutation audit log';


-- Seed the reserved roles for every existing tenant. New tenants receive the
-- same rows through the bootstrap path before interactive administration.
INSERT IGNORE INTO roles (tenant_id, role_code, role_name, remark, role_state, system_flag, created_by, updated_by)
SELECT tenant_id, 'reader', 'Reader', 'Read-only tenant role', 'active', 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants;
INSERT IGNORE INTO roles (tenant_id, role_code, role_name, remark, role_state, system_flag, created_by, updated_by)
SELECT tenant_id, 'writer', 'Writer', 'Writer inherits reader', 'active', 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants;
INSERT IGNORE INTO roles (tenant_id, role_code, role_name, remark, role_state, system_flag, created_by, updated_by)
SELECT tenant_id, 'admin', 'Administrator', 'Administrator inherits writer', 'active', 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants;


-- Seed the deployment-owned menu catalog once per tenant. Fixed IDs match the
-- SmartAdmin compatibility constants in internal/smartweb.
INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT tenant_id, 810000001, 0, 'Access Control', 1, 200, '', '', '', '', 'SafetyCertificateOutlined', 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants;

INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT tenant_id, 810000002, 810000001, 'Role Management', 2, 201, '/system/role', 'system/role/index.vue', '', '', 'SafetyCertificateOutlined', 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants;

INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT tenant_id, 810000003, 810000001, 'Menu Catalog', 2, 202, '/system/menu', 'system/menu/menu-list.vue', '', '', 'UnorderedListOutlined', 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants;

-- Capability function points. Every ManagedCapabilities() value must appear
-- exactly once so a saved policy is always representable in the role tree.
INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, 810000002, x.menu_name, 3, x.sort_order, '', '', '', x.capability, '', 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000017 AS menu_id, 'Update related Projects' AS menu_name, 'project.update' AS capability, 217 AS sort_order
    UNION ALL SELECT 810000018, 'Delete owned Projects', 'project.delete', 218
    UNION ALL SELECT 810000019, 'Manage all tenant Projects (scope only)', 'project.manage.any', 219
    UNION ALL SELECT 810000031, 'View tenant roles and employees', 'authorization.role.read', 231
    UNION ALL SELECT 810000032, 'Edit Project read scope', 'authorization.role.scope.manage', 232
    UNION ALL SELECT 810000033, 'Assign tenant roles', 'authorization.role.member.add', 233
    UNION ALL SELECT 810000034, 'Remove tenant roles', 'authorization.role.member.remove', 234
    UNION ALL SELECT 810000035, 'Read policy audit events', 'authorization.policy.events.read', 235
    UNION ALL SELECT 810000036, 'Manage tenant users', 'system.user.manage', 236
    UNION ALL SELECT 810000037, 'Manage tenant menus', 'system.menu.manage', 237
    UNION ALL SELECT 810000011, 'Read related Projects', 'project.read', 211
    UNION ALL SELECT 810000012, 'Read all tenant Projects', 'project.read.any', 212
    UNION ALL SELECT 810000013, 'Manage Project collaborators', 'project.member.manage', 213
    UNION ALL SELECT 810000014, 'Edit role permissions', 'authorization.role.manage', 214
    UNION ALL SELECT 810000015, 'Call Greeter', 'greeter.hello', 215
    UNION ALL SELECT 810000016, 'Watch Greeter stream', 'greeter.watch', 216
) AS x;

-- Current-operator management buttons. They carry webPerms only and can never
-- be saved as a target-role capability; the application excludes them from
-- role-menu grant writes.
INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, 810000002, x.menu_name, 3, x.sort_order, '', '', x.web_perms, '', '', 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000021 AS menu_id, 'Manage roles' AS menu_name, 'system:role:menu:update' AS web_perms, 221 AS sort_order
    UNION ALL SELECT 810000022, 'Manage Project data scopes', 'system:role:dataScope:update', 222
    UNION ALL SELECT 810000023, 'Add role employees', 'system:role:employee:add', 223
    UNION ALL SELECT 810000024, 'Remove role employees', 'system:role:employee:delete', 224
    UNION ALL SELECT 810000025, 'Batch remove role employees', 'system:role:employee:batch:delete', 207
) AS x;
