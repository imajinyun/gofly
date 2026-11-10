-- Reverse organization management. Identity memberships and RBAC rows
-- are intentionally retained; organization columns and tables are removed.

ALTER TABLE tenant_users
    DROP FOREIGN KEY fk_tenant_users_department,
    DROP FOREIGN KEY fk_tenant_users_position,
    DROP KEY idx_tenant_users_department,
    DROP COLUMN department_id,
    DROP COLUMN position_id,
    DROP COLUMN remark;

ALTER TABLE users
    DROP COLUMN gender,
    DROP COLUMN phone,
    DROP COLUMN email;

ALTER TABLE menus
    DROP COLUMN frame_url,
    DROP COLUMN frame_flag,
    DROP COLUMN cache_flag;

DROP TABLE IF EXISTS positions;
DROP TABLE IF EXISTS departments;
