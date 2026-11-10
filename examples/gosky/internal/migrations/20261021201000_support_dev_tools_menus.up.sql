-- Menu seeds for the development-tool demo set.
INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, 0, x.menu_name, x.menu_type, x.sort_order, x.path, x.component, '', '', x.icon, 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000155 AS menu_id, 'API Encrypt Demo' AS menu_name, 2 AS menu_type, 415 AS sort_order,
           '/support/api-encrypt' AS path, 'support/api-encrypt/api-encrypt-index.vue' AS component,
           'SafetyOutlined' AS icon
    UNION ALL SELECT 810000156, 'Heart Beat', 2, 416,
           '/support/heart-beat', 'support/heart-beat/heart-beat-list.vue',
           'MonitorOutlined'
    UNION ALL SELECT 810000157, 'Change Log', 2, 417,
           '/support/change-log', 'support/change-log/change-log-list.vue',
           'ContainerOutlined'
    UNION ALL SELECT 810000158, 'Code Generator', 2, 418,
           '/support/code-generator', 'support/code-generator/code-generator-list.vue',
           'CodeOutlined'
) AS x;

INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, x.parent_id, x.menu_name, 3, x.sort_order, '', '', x.web_perms, x.capability, '', 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000161 AS menu_id, 810000002 AS parent_id, 'View dev tools' AS menu_name, 'support.devTools.read' AS capability, '' AS web_perms, 242 AS sort_order
    UNION ALL SELECT 810000162, 810000002, 'View change logs', 'support.changeLog.read', '', 243
    UNION ALL SELECT 810000163, 810000002, 'Manage change logs', 'support.changeLog.manage', '', 244
    UNION ALL SELECT 810000164, 810000157, 'Query change logs', '', 'support:changeLog:query', 521
    UNION ALL SELECT 810000165, 810000157, 'Add change log', '', 'support:changeLog:add', 522
    UNION ALL SELECT 810000166, 810000157, 'Update change log', '', 'support:changeLog:update', 523
    UNION ALL SELECT 810000167, 810000157, 'Delete change log', '', 'support:changeLog:delete', 524
    UNION ALL SELECT 810000168, 810000157, 'Batch delete change logs', '', 'support:changeLog:batchDelete', 525
) AS x;
