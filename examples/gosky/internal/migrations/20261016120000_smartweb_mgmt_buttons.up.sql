-- Complete the current-operator button catalog required by the unmodified
-- SmartAdmin role and menu pages. The original dynamic_rbac migration seeded
-- only the five buttons used by the role-permission panes; the unchanged
-- pages additionally gate role add/edit/delete and menu add/edit/delete
-- behind six more webPerms points (v-privilege).
--
-- Like the earlier five, these rows are system operator buttons:
-- - they carry web_perms only and never a capability, so they cannot be
--   saved as a target-role grant (resolveGrantMenus rejects them);
-- - login navigation projects them only after the same capability check the
--   mutation handler enforces (authorization.role.manage / system.menu.manage).
--
-- IDs 810000026..28 and 810000041..43 stay inside the SmartAdmin hardcoded
-- 810000xxx range; tenant-allocated menus begin at 820000000.

INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, x.parent_id, x.menu_name, 3, x.sort_order, '', '', x.web_perms, '', '', 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000026 AS menu_id, 810000002 AS parent_id, 'Add role' AS menu_name, 'system:role:add' AS web_perms, 208 AS sort_order
    UNION ALL SELECT 810000027, 810000002, 'Edit role', 'system:role:update', 209
    UNION ALL SELECT 810000028, 810000002, 'Delete role', 'system:role:delete', 210
    UNION ALL SELECT 810000041, 810000003, 'Add menu', 'system:menu:add', 301
    UNION ALL SELECT 810000042, 810000003, 'Edit menu', 'system:menu:update', 302
    UNION ALL SELECT 810000043, 810000003, 'Batch delete menus', 'system:menu:batchDelete', 303
) AS x;
