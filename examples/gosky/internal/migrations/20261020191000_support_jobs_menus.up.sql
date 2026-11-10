-- Menu seeds for the scheduled job console.
INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, 0, x.menu_name, x.menu_type, x.sort_order, x.path, x.component, '', '', x.icon, 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000145 AS menu_id, 'Scheduled Jobs' AS menu_name, 2 AS menu_type, 414 AS sort_order,
           '/support/job' AS path, 'support/job/job-list.vue' AS component,
           'FieldTimeOutlined' AS icon
) AS x;

INSERT IGNORE INTO menus
    (tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, system_flag, created_by, updated_by)
SELECT m.tenant_id, x.menu_id, x.parent_id, x.menu_name, 3, x.sort_order, '', '', x.web_perms, x.capability, '', 1, 1, 'system-bootstrap', 'system-bootstrap'
FROM tenants AS m
JOIN (
    SELECT 810000146 AS menu_id, 810000002 AS parent_id, 'View jobs' AS menu_name, 'support.job.read' AS capability, '' AS web_perms, 239 AS sort_order
    UNION ALL SELECT 810000147, 810000002, 'Manage jobs', 'support.job.manage', '', 240
    UNION ALL SELECT 810000148, 810000002, 'Execute jobs', 'support.job.execute', '', 241
    UNION ALL SELECT 810000149, 810000145, 'Query jobs', '', 'support:job:query', 511
    UNION ALL SELECT 810000150, 810000145, 'Add job', '', 'support:job:add', 512
    UNION ALL SELECT 810000151, 810000145, 'Update job', '', 'support:job:update', 513
    UNION ALL SELECT 810000152, 810000145, 'Execute job', '', 'support:job:execute', 514
    UNION ALL SELECT 810000153, 810000145, 'Query job logs', '', 'support:job:log:query', 515
    UNION ALL SELECT 810000154, 810000145, 'Delete job', '', 'support:job:log:delete', 516
) AS x;
