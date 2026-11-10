-- Menu seeds for the in-process cache console and the SmartReload console.
INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, 0, x.menu_name, x.menu_type, x.sort_order, x.path, x.component, '', '', x.icon, 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000134 AS menu_id, 'Caches' AS menu_name, 2 AS menu_type, 412 AS sort_order,
           '/support/cache' AS path, 'support/cache/cache-list.vue' AS component,
           'AppstoreOutlined' AS icon
    UNION ALL SELECT 810000137, 'Smart Reload', 2, 413,
           '/support/reload', 'support/reload/reload-list.vue',
           'ReloadOutlined'
) AS x;

INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, x.parent_id, x.menu_name, 3, x.sort_order, '', '', x.web_perms, x.capability, '', 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000135 AS menu_id, 810000002 AS parent_id, 'View caches' AS menu_name, 'support.cache.read' AS capability, '' AS web_perms, 235 AS sort_order
    UNION ALL SELECT 810000136, 810000002, 'Manage caches', 'support.cache.manage', '', 236
    UNION ALL SELECT 810000139, 810000002, 'View reloads', 'support.reload.read', '', 237
    UNION ALL SELECT 810000140, 810000002, 'Manage reloads', 'support.reload.manage', '', 238
    UNION ALL SELECT 810000141, 810000134, 'Inspect cache keys', '', 'support:cache:keys', 501
    UNION ALL SELECT 810000142, 810000134, 'Clear cache', '', 'support:cache:delete', 502
    UNION ALL SELECT 810000143, 810000137, 'Execute reload', '', 'support:reload:execute', 503
    UNION ALL SELECT 810000144, 810000137, 'View reload results', '', 'support:reload:result', 504
) AS x;
