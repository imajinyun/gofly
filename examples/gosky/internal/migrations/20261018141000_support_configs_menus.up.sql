-- Menu seeds for the runtime configuration console.
INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, 0, x.menu_name, x.menu_type, x.sort_order, x.path, x.component, '', '', x.icon, 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000010 AS menu_id, 'Configuration' AS menu_name, 2 AS menu_type, 403 AS sort_order,
           '/support/config' AS path, 'support/config/config-list.vue' AS component, 'SettingOutlined' AS icon
) AS x;

INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, x.parent_id, x.menu_name, 3, x.sort_order, '', '', x.web_perms, x.capability, '', 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000091 AS menu_id, 810000002 AS parent_id, 'View configuration' AS menu_name, 'support.config.read' AS capability, '' AS web_perms, 223 AS sort_order
    UNION ALL SELECT 810000092, 810000002, 'Manage configuration', 'support.config.manage', '', 224
    UNION ALL SELECT 810000093, 810000010, 'Query configuration', '', 'support:config:query', 431
    UNION ALL SELECT 810000094, 810000010, 'Add configuration', '', 'support:config:add', 432
    UNION ALL SELECT 810000095, 810000010, 'Edit configuration', '', 'support:config:update', 433
) AS x;
