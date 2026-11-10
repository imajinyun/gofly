-- Menu seeds for the OA notice board.
INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, 0, x.menu_name, 2, 419, x.path, x.component, '', '', 'NotificationOutlined', 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000170 AS menu_id, 'Notice Board' AS menu_name,
           '/oa/notice' AS path, 'business/oa/notice/notice-list.vue' AS component
) AS x;

INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, x.parent_id, x.menu_name, 3, x.sort_order, '', '', x.web_perms, x.capability, '', 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000171 AS menu_id, 810000002 AS parent_id, 'View notices' AS menu_name, 'oa.notice.read' AS capability, '' AS web_perms, 245 AS sort_order
    UNION ALL SELECT 810000172, 810000002, 'Manage notices', 'oa.notice.manage', '', 246
    UNION ALL SELECT 810000173, 810000170, 'Query notices', '', 'oa:notice:query', 531
    UNION ALL SELECT 810000174, 810000170, 'Add notice', '', 'oa:notice:add', 532
    UNION ALL SELECT 810000175, 810000170, 'Update notice', '', 'oa:notice:update', 533
    UNION ALL SELECT 810000176, 810000170, 'Delete notice', '', 'oa:notice:delete', 534
    UNION ALL SELECT 810000177, 810000170, 'Query notice types', '', 'oa:noticeType:query', 535
    UNION ALL SELECT 810000178, 810000170, 'Manage notice types', '', 'oa:noticeType:manage', 536
) AS x;
