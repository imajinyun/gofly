-- Menu seeds for the serial number console.
INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, 0, x.menu_name, x.menu_type, x.sort_order, x.path, x.component, '', '', x.icon, 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000105 AS menu_id, 'Serial Numbers' AS menu_name, 2 AS menu_type, 404 AS sort_order,
           '/support/serial-number' AS path, 'support/serial-number/serial-number-list.vue' AS component,
           'OrderedListOutlined' AS icon
) AS x;

INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, x.parent_id, x.menu_name, 3, x.sort_order, '', '', x.web_perms, x.capability, '', 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000101 AS menu_id, 810000002 AS parent_id, 'View serial numbers' AS menu_name, 'support.serialNumber.read' AS capability, '' AS web_perms, 225 AS sort_order
    UNION ALL SELECT 810000102, 810000002, 'Generate serial numbers', 'support.serialNumber.generate', '', 226
    UNION ALL SELECT 810000103, 810000105, 'View serial records', '', 'support:serialNumber:record', 441
    UNION ALL SELECT 810000104, 810000105, 'Generate serial number', '', 'support:serialNumber:generate', 442
) AS x;
