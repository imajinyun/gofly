-- Expose the unmodified SmartAdmin Employee, Department, and Position pages in
-- the login navigation, along with every webPerms button those pages gate via
-- v-privilege. The backend organization endpoints already exist and all
-- require the system.user.manage capability, so every new operator button
-- maps to that capability during login projection.
--
-- Structural pages (types 1 and 2) carry path/component identifiers that only
-- reference compiled views shipped with smart-web. The function points carry
-- web_perms only: like the earlier operator buttons they are system rows, are
-- not editable, and resolveGrantMenus rejects them as target-role grants.
--
-- IDs stay inside the SmartAdmin hardcoded 810000xxx range; tenant-allocated
-- menus begin at 820000000.

-- Organization catalog and the three organization pages.
INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, x.parent_id, x.menu_name, x.menu_type, x.sort_order, x.path, x.component, '', '', x.icon, 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000004 AS menu_id, 0 AS parent_id, 'Organization' AS menu_name, 1 AS menu_type, 300 AS sort_order, '' AS path, '' AS component, 'TeamOutlined' AS icon
    UNION ALL SELECT 810000005, 810000004, 'Employee Management', 2, 301, '/system/employee', 'system/employee/index.vue', 'TeamOutlined'
    UNION ALL SELECT 810000006, 810000004, 'Department Management', 2, 302, '/system/department', 'system/department/department-list.vue', 'ApartmentOutlined'
    UNION ALL SELECT 810000007, 810000004, 'Position Management', 2, 303, '/system/position', 'system/position/position-list.vue', 'IdcardOutlined'
) AS x;

-- Current-operator buttons for the organization pages.
INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, x.parent_id, x.menu_name, 3, x.sort_order, '', '', x.web_perms, '', '', 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000061 AS menu_id, 810000005 AS parent_id, 'Add employee' AS menu_name, 'system:employee:add' AS web_perms, 311 AS sort_order
    UNION ALL SELECT 810000062, 810000005, 'Edit employee', 'system:employee:update', 312
    UNION ALL SELECT 810000063, 810000005, 'Delete employees', 'system:employee:delete', 313
    UNION ALL SELECT 810000064, 810000005, 'Move employee department', 'system:employee:department:update', 314
    UNION ALL SELECT 810000065, 810000005, 'Toggle employee state', 'system:employee:disabled', 315
    UNION ALL SELECT 810000066, 810000005, 'Reset employee password', 'system:employee:password:reset', 316
    UNION ALL SELECT 810000051, 810000006, 'Query departments', 'support:department:query', 321
    UNION ALL SELECT 810000052, 810000006, 'Add department', 'system:department:add', 322
    UNION ALL SELECT 810000053, 810000006, 'Edit department', 'system:department:update', 323
    UNION ALL SELECT 810000054, 810000006, 'Delete department', 'system:department:delete', 324
) AS x;
