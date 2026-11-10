-- Remove the file console seeds first (FK to tenants and role_menus FK),
-- then the metadata table. Stored bytes on the local filesystem are removed
-- by operations tooling, never by a migration (data loss guard).
DELETE FROM role_menus WHERE menu_id IN (810000008, 810000071, 810000072, 810000073);
DELETE FROM menus WHERE menu_id IN (810000008, 810000071, 810000072, 810000073);
DROP TABLE IF EXISTS support_files;
