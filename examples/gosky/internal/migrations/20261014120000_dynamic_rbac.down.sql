-- Reverse the dynamic RBAC presentation layer. casbin_rules and
-- casbin_policy_events are deliberately untouched: historical p/g rows remain
-- interpretable by the fixed-role code after rollback, even when they
-- reference a retired custom role code.
DROP TABLE IF EXISTS admin_audit_logs;
DROP TABLE IF EXISTS role_menus;
DROP TABLE IF EXISTS menus;
DROP TABLE IF EXISTS roles;
