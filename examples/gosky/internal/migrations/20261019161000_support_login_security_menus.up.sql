-- Menu seeds for login logs, fail-lock protection and the level-3
-- security protect configuration console.
INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, 0, x.menu_name, x.menu_type, x.sort_order, x.path, x.component, '', '', x.icon, 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000124 AS menu_id, 'Login Logs' AS menu_name, 2 AS menu_type, 408 AS sort_order,
           '/support/login-log' AS path, 'support/login-log/login-log-list.vue' AS component,
           'HistoryOutlined' AS icon
    UNION ALL SELECT 810000125, 'Login Fail Locks', 2, 409,
           '/support/login-fail', 'support/login-fail/login-fail-list.vue',
           'LockOutlined'
    UNION ALL SELECT 810000126, 'Level-3 Protection', 2, 410,
           '/support/level3protect', 'support/level3protect/level3-protect-config-index.vue',
           'SafetyCertificateOutlined'
) AS x;

INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, x.parent_id, x.menu_name, 3, x.sort_order, '', '', x.web_perms, x.capability, '', 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000127 AS menu_id, 810000002 AS parent_id, 'View login logs' AS menu_name, 'support.loginLog.read' AS capability, '' AS web_perms, 232 AS sort_order
    UNION ALL SELECT 810000128, 810000002, 'Manage login protection', 'support.loginLog.manage', '', 233
    UNION ALL SELECT 810000129, 810000124, 'Query login logs', '', 'support:loginLog:query', 481
) AS x;
