ALTER TABLE auth_login_logs
    MODIFY COLUMN operation ENUM(
        'login',
        'refresh',
        'logout',
        'authenticate',
        'session_list',
        'session_revoke'
    ) NOT NULL COMMENT 'Authentication or self-service session operation';

CREATE INDEX idx_auth_sessions_owner_active_seen
    ON auth_sessions (user_id, tenant_id, realm, revoked_at, expires_at, last_seen_at DESC, session_id);
