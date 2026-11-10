-- File service metadata for the SmartAdmin /support/file/** endpoints.
-- Binary bytes stay on the local filesystem outside the database; this table
-- records only immutable metadata and the tenant-scoped storage key.
--
-- MySQL 8.0+. Depends on identity/tenant and dynamic_rbac migrations.
CREATE TABLE support_files (
    file_id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'File primary key',
    tenant_id         BIGINT UNSIGNED NOT NULL COMMENT 'Owning tenant',
    folder_type       TINYINT UNSIGNED NOT NULL COMMENT 'SmartAdmin folder type 1 common 2 notice 3 help-doc 4 feedback',
    file_name         VARCHAR(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'Original file name',
    file_key          VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Storage-relative key, uuid prefixed',
    file_size         BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Size in bytes',
    file_type         VARCHAR(128) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'MIME type',
    creator_id        BIGINT UNSIGNED NOT NULL COMMENT 'users.user_id of uploader',
    creator_name      VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Uploader name snapshot',
    created_at        DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT 'Creation time',
    updated_at        DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT 'Last update time',

    PRIMARY KEY (file_id),
    UNIQUE KEY uk_support_files_key (tenant_id, file_key),
    KEY idx_support_files_tenant_folder (tenant_id, folder_type, created_at),
    KEY idx_support_files_tenant_name (tenant_id, file_name),
    CONSTRAINT fk_support_files_tenant FOREIGN KEY (tenant_id) REFERENCES tenants (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Tenant file metadata for the SmartAdmin file service';

-- System structural page and current-operator buttons for the file console.
INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, 0, x.menu_name, x.menu_type, x.sort_order, x.path, x.component, '', '', x.icon, 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000008 AS menu_id, 'Files' AS menu_name, 2 AS menu_type, 401 AS sort_order,
           '/support/file' AS path, 'support/file/file-list.vue' AS component, 'FolderOutlined' AS icon
) AS x;

INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, x.parent_id, x.menu_name, 3, x.sort_order, '', '', x.web_perms, x.capability, '', 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000071 AS menu_id, 810000008 AS parent_id, 'View files' AS menu_name, '' AS web_perms, 'support.file.read' AS capability, 411 AS sort_order
    UNION ALL SELECT 810000072, 810000008, 'Upload and manage files', '', 'support.file.manage', 412
    UNION ALL SELECT 810000073, 810000008, 'Query files', 'support:file:query', '', 413
) AS x;
