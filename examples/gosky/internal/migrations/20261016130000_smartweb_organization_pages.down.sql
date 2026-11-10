-- Remove the organization operator buttons first to satisfy the role_menus
-- foreign key, then the organization pages and catalog. System operator
-- buttons are rejected as target-role grants, so role_menus is expected to
-- contain no matching rows.
DELETE FROM role_menus
WHERE menu_id BETWEEN 810000051 AND 810000066;

DELETE FROM menus
WHERE menu_id IN (810000004, 810000005, 810000006, 810000007)
   OR (menu_id BETWEEN 810000051 AND 810000066);
