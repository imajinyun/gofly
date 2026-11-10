-- Local development seed for the unmodified SmartAdmin console.
--
-- Creates exactly one tenant and one administrator identity so the SmartAdmin
-- /login form (which submits no tenant code) resolves a unique admin
-- membership. Run AFTER every migration and BEFORE gosky-policy-bootstrap:
--
--   mysql gosky < etc/seed.dev.sql
--
-- Default console credentials (local only; change for any shared database):
--   loginName: admin
--   password:  Admin@12345
--
-- The hash is an Argon2id PHC string produced by internal/authn.HashPassword
-- (m=65536, t=3, p=2). Re-generate with the project hasher instead of editing
-- the value by hand. INSERT IGNORE keeps re-runs idempotent.

INSERT IGNORE INTO tenants (tenant_id, tenant_code, tenant_name, tenant_state)
VALUES (1001, 'dev-local', 'Local development tenant', 'active');

INSERT IGNORE INTO users (user_id, subject, login_name, password_hash, account_type, user_state, display_name)
VALUES (
    1001,
    'user:admin',
    'admin',
    '$argon2id$v=19$m=65536,t=3,p=2$aCMhVC9F0RGOa9qv5hJr2w$IpjaWtBV/U3uoNl2e9+iA6TeJfDuH9Yw23urH08mGGA',
    'both',
    'active',
    'Gosky Admin'
);

INSERT IGNORE INTO tenant_users (tenant_id, user_id, member_realm, member_state, display_name)
VALUES (1001, 1001, 'both', 'active', 'Gosky Admin');
