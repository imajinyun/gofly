-- Menu seeds for the operation audit console.
INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, 0, x.menu_name, x.menu_type, x.sort_order, x.path, x.component, '', '', x.icon, 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000130 AS menu_id, 'Operate Logs' AS menu_name, 2 AS menu_type, 411 AS sort_order,
           '/support/operate-log' AS path, 'support/operate-log/operate-log-list.vue' AS component,
           'ProfileOutlined' AS icon
) AS x;

INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, x.parent_id, x.menu_name, 3, x.sort_order, '', '', x.web_perms, x.capability, '', 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000131 AS menu_id, 810000002 AS parent_id, 'View operate logs' AS menu_name, 'support.operateLog.read' AS capability, '' AS web_perms, 234 AS sort_order
    UNION ALL SELECT 810000132, 810000130, 'Query operate logs', '', 'support:operateLog:query', 491
    UNION ALL SELECT 810000133, 810000130, 'View operate log detail', '', 'support:operateLog:detail', 492
) AS x;
