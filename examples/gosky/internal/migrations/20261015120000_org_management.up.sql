-- Tenant organization management for the SmartAdmin console:
-- departments, positions, employee profile fields, and presentation
-- metadata on custom menus. Every organization row is tenant scoped;
-- subjects remain canonical users/tenant_users rows.
--
-- Assumptions:
-- - MySQL 8.0+
-- - The identity, RBAC, and dynamic_rbac migrations already applied.
-- - Identity membership is the authority for login; departments and
--   positions are presentation and grouping data only.

-- Departments: a per-tenant tree. The seeded root is deployment-owned.
CREATE TABLE departments (
    department_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Department primary key',
    tenant_id     BIGINT UNSIGNED NOT NULL COMMENT 'Owning tenant',
    name          VARCHAR(50)  COLLATE utf8mb4_bin NOT NULL COMMENT 'Department name',
    manager_id    BIGINT UNSIGNED NULL COMMENT 'Manager users.user_id',
    parent_id     BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Parent department, 0 for root',
    sort          INT NOT NULL DEFAULT 0 COMMENT 'Display sort, smaller first',
    state         ENUM('active','disabled','deleted') NOT NULL DEFAULT 'active' COMMENT 'Lifecycle state',
    system_flag   TINYINT(1) NOT NULL DEFAULT 0 COMMENT 'Seeded immutable row when true',
    created_at    DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT 'Creation time',
    updated_at    DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT 'Last update time',

    PRIMARY KEY (department_id),
    UNIQUE KEY uk_departments_tenant_name (tenant_id, name),
    UNIQUE KEY uk_departments_tenant_id (tenant_id, department_id),
    KEY idx_departments_tenant_parent (tenant_id, parent_id, state),
    CONSTRAINT fk_departments_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Tenant-scoped departments';

-- Positions (job titles) are a flat per-tenant list.
CREATE TABLE positions (
    position_id   BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Position primary key',
    tenant_id     BIGINT UNSIGNED NOT NULL COMMENT 'Owning tenant',
    name          VARCHAR(50)  COLLATE utf8mb4_bin NOT NULL COMMENT 'Position name',
    level         VARCHAR(32) COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'Free-form position level',
    sort          INT NOT NULL DEFAULT 0 COMMENT 'Display sort, smaller first',
    remark        VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Remark',
    state         ENUM('active','disabled','deleted') NOT NULL DEFAULT 'active' COMMENT 'Lifecycle state',
    system_flag   TINYINT(1) NOT NULL DEFAULT 0 COMMENT 'Seeded immutable row when true',
    created_at    DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT 'Creation time',
    updated_at    DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT 'Last update time',

    PRIMARY KEY (position_id),
    UNIQUE KEY uk_positions_tenant_name (tenant_id, name),
    UNIQUE KEY uk_positions_tenant_id (tenant_id, position_id),
    KEY idx_positions_tenant_state (tenant_id, state),
    CONSTRAINT fk_positions_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Tenant-scoped positions';

-- Membership placement inside the tenant organization.
ALTER TABLE tenant_users
    ADD COLUMN department_id BIGINT UNSIGNED NULL COMMENT 'Department placement' AFTER display_name,
    ADD COLUMN position_id   BIGINT UNSIGNED NULL COMMENT 'Position placement' AFTER department_id,
    ADD COLUMN remark        VARCHAR(200) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Membership remark' AFTER position_id,
    ADD KEY idx_tenant_users_department (tenant_id, department_id),
    ADD CONSTRAINT fk_tenant_users_department FOREIGN KEY (tenant_id, department_id)
        REFERENCES departments (tenant_id, department_id),
    ADD CONSTRAINT fk_tenant_users_position FOREIGN KEY (tenant_id, position_id)
        REFERENCES positions (tenant_id, position_id);

-- Global profile fields shared across tenants. Avatar uses the
-- existing avatar_url column; no duplicate avatar column is created.
ALTER TABLE users
    ADD COLUMN gender TINYINT NOT NULL DEFAULT 0 COMMENT 'Gender 0 unknown 1 male 2 female' AFTER display_name,
    ADD COLUMN phone  VARCHAR(20)  CHARACTER SET ascii COLLATE ascii_bin NULL COMMENT 'Phone number' AFTER gender,
    ADD COLUMN email  VARCHAR(100) COLLATE utf8mb4_bin NULL COMMENT 'Email address' AFTER phone;

-- Presentation metadata used by the SmartAdmin menu editor.
ALTER TABLE menus
    ADD COLUMN frame_url  VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'External link URL when frame_flag is set' AFTER path,
    ADD COLUMN frame_flag TINYINT(1) NOT NULL DEFAULT 0 COMMENT 'Opens as an external frame when true' AFTER frame_url,
    ADD COLUMN cache_flag TINYINT(1) NOT NULL DEFAULT 0 COMMENT 'Browser keep-alive cache when true' AFTER frame_flag;

-- Seed a root department for every existing tenant so employee creation
-- always has a valid placement target.
INSERT INTO departments (tenant_id, name, parent_id, sort, state, system_flag)
SELECT tenant_id, '总公司', 0, 1, 'active', 1 FROM tenants;
