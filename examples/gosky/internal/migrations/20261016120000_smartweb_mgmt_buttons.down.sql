-- Remove the six operator buttons added for the SmartAdmin role and menu
-- pages. Grant rows are deleted first to satisfy the role_menus foreign key,
-- although system operator buttons are rejected as target-role grants and
-- role_menus is expected to contain no matching rows.
DELETE FROM role_menus
WHERE menu_id IN (810000026, 810000027, 810000028, 810000041, 810000042, 810000043);

DELETE FROM menus
WHERE menu_id IN (810000026, 810000027, 810000028, 810000041, 810000042, 810000043);
