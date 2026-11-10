-- Menu seeds for the data-dictionary console.
-- Structural page 810000009; capability points for role grants; webPerms
-- buttons for the unchanged client's v-privilege directives.
INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, 0, x.menu_name, x.menu_type, x.sort_order, x.path, x.component, '', '', x.icon, 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000009 AS menu_id, 'Dictionary' AS menu_name, 2 AS menu_type, 402 AS sort_order,
           '/support/dict' AS path, 'support/dict/index.vue' AS component, 'BookOutlined' AS icon
) AS x;

INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, x.parent_id, x.menu_name, 3, x.sort_order, '', '', x.web_perms, x.capability, '', 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000081 AS menu_id, 810000002 AS parent_id, 'View dictionaries' AS menu_name, 'support.dict.read' AS capability, '' AS web_perms, 421 AS sort_order
    UNION ALL SELECT 810000082, 810000002, 'Manage dictionaries', 'support.dict.manage', '', 422
    UNION ALL SELECT 810000083, 810000009, 'Add dictionary', '', 'support:dict:add', 423
    UNION ALL SELECT 810000084, 810000009, 'Edit dictionary', '', 'support:dict:update', 424
    UNION ALL SELECT 810000085, 810000009, 'Delete dictionary', '', 'support:dict:delete', 425
    UNION ALL SELECT 810000086, 810000009, 'Add dictionary value', '', 'support:dictData:add', 426
    UNION ALL SELECT 810000087, 810000009, 'Edit dictionary value', '', 'support:dictData:update', 427
    UNION ALL SELECT 810000088, 810000009, 'Delete dictionary value', '', 'support:dictData:delete', 428
) AS x;
