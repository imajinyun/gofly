-- Menu seeds for the feedback console.
INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, 0, x.menu_name, x.menu_type, x.sort_order, x.path, x.component, '', '', x.icon, 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000111 AS menu_id, 'Feedback' AS menu_name, 2 AS menu_type, 406 AS sort_order,
           '/support/feedback' AS path, 'support/feedback/feedback-list.vue' AS component,
           'CommentOutlined' AS icon
) AS x;

INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, x.parent_id, x.menu_name, 3, x.sort_order, '', '', x.web_perms, x.capability, '', 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000112 AS menu_id, 810000002 AS parent_id, 'View feedback' AS menu_name, 'support.feedback.read' AS capability, '' AS web_perms, 229 AS sort_order
    UNION ALL SELECT 810000113, 810000111, 'Query feedback', '', 'feedback:query', 461
) AS x;
