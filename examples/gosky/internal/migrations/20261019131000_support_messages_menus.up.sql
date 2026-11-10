-- Menu seeds for the in-app message console.
INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, 0, x.menu_name, x.menu_type, x.sort_order, x.path, x.component, '', '', x.icon, 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000106 AS menu_id, 'Messages' AS menu_name, 2 AS menu_type, 405 AS sort_order,
           '/support/message' AS path, 'support/message/message-list.vue' AS component,
           'MessageOutlined' AS icon
) AS x;

INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, x.parent_id, x.menu_name, 3, x.sort_order, '', '', x.web_perms, x.capability, '', 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000107 AS menu_id, 810000002 AS parent_id, 'View messages' AS menu_name, 'support.message.read' AS capability, '' AS web_perms, 227 AS sort_order
    UNION ALL SELECT 810000108, 810000002, 'Send and delete messages', 'support.message.send', '', 228
    UNION ALL SELECT 810000109, 810000106, 'Send message', '', 'support:message:send', 451
    UNION ALL SELECT 810000110, 810000106, 'Delete message', '', 'support:message:delete', 452
) AS x;
