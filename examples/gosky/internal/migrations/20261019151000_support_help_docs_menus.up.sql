-- Menu seeds for the help document console.
INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, 0, x.menu_name, x.menu_type, x.sort_order, x.path, x.component, '', '', x.icon, 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000114 AS menu_id, 'Help Docs' AS menu_name, 2 AS menu_type, 407 AS sort_order,
           '/support/help-doc' AS path, 'support/help-doc/management/help-doc-manage-list.vue' AS component,
           'QuestionCircleOutlined' AS icon
) AS x;

INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, x.parent_id, x.menu_name, 3, x.sort_order, '', '', x.web_perms, x.capability, '', 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000115 AS menu_id, 810000002 AS parent_id, 'View help docs' AS menu_name, 'support.helpDoc.read' AS capability, '' AS web_perms, 230 AS sort_order
    UNION ALL SELECT 810000116, 810000002, 'Manage help docs', 'support.helpDoc.manage', '', 231
    UNION ALL SELECT 810000117, 810000114, 'Query help docs', '', 'support:helpDoc:query', 471
    UNION ALL SELECT 810000118, 810000114, 'Add help doc', '', 'support:helpDoc:add', 472
    UNION ALL SELECT 810000119, 810000114, 'Edit help doc', '', 'support:helpDoc:update', 473
    UNION ALL SELECT 810000120, 810000114, 'Delete help doc', '', 'support:helpDoc:delete', 474
    UNION ALL SELECT 810000121, 810000114, 'Add help doc catalog', '', 'support:helpDocCatalog:addCategory', 475
    UNION ALL SELECT 810000122, 810000114, 'Edit help doc catalog', '', 'support:helpDocCatalog:edit', 476
    UNION ALL SELECT 810000123, 810000114, 'Delete help doc catalog', '', 'support:helpDocCatalog:delete', 477
) AS x;
