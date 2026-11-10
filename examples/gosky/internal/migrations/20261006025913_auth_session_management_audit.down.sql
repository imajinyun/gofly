DROP INDEX idx_auth_sessions_owner_active_seen ON auth_sessions;

ALTER TABLE auth_login_logs
    MODIFY COLUMN operation ENUM(
        'login',
        'refresh',
        'logout',
        'authenticate'
    ) NOT NULL COMMENT 'Authentication operation';
