-- write forward migration SQL here
-- Gosky-owned identity, tenant membership, and refresh-session storage.
-- This migration intentionally does not add foreign keys from the pre-existing
-- Project tables: they may already contain deployment-owned tenant IDs and
-- subject values before interactive gosky accounts are introduced.

CREATE TABLE tenants (
    tenant_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Tenant ID',
    tenant_code VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Stable tenant code',
    tenant_name VARCHAR(255) NOT NULL COMMENT 'Tenant display name',
    tenant_state ENUM('active', 'disabled') NOT NULL DEFAULT 'active' COMMENT 'Tenant state',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT 'Creation time',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT 'Last update time',

    PRIMARY KEY (tenant_id),
    UNIQUE KEY uk_tenants_code (tenant_code),
    KEY idx_tenants_state (tenant_state, tenant_id),
    CHECK (tenant_code <> '')
) ENGINE=InnoDB
  DEFAULT CHARSET=utf8mb4
  COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Gosky tenants';


CREATE TABLE users (
    user_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'User ID',
    subject VARCHAR(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'Canonical authorization subject',
    login_name VARCHAR(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'Unique login name',
    password_hash VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Argon2id password hash',
    account_type ENUM('admin', 'front', 'both') NOT NULL DEFAULT 'both' COMMENT 'Permitted identity realms',
    user_state ENUM('active', 'disabled') NOT NULL DEFAULT 'active' COMMENT 'Account state',
    display_name VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'Display name',
    avatar_url VARCHAR(2048) NOT NULL DEFAULT '' COMMENT 'Avatar URL',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT 'Creation time',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT 'Last update time',

    PRIMARY KEY (user_id),
    UNIQUE KEY uk_users_subject (subject),
    UNIQUE KEY uk_users_login_name (login_name),
    KEY idx_users_state_type (user_state, account_type, user_id),
    CHECK (subject <> ''),
    CHECK (login_name <> ''),
    CHECK (password_hash <> '')
) ENGINE=InnoDB
  DEFAULT CHARSET=utf8mb4
  COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Gosky interactive user accounts';


CREATE TABLE tenant_users (
    tenant_user_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Tenant membership ID',
    tenant_id BIGINT UNSIGNED NOT NULL COMMENT 'Tenant ID',
    user_id BIGINT UNSIGNED NOT NULL COMMENT 'User ID',
    member_realm ENUM('admin', 'front', 'both') NOT NULL DEFAULT 'both' COMMENT 'Permitted realms in this tenant',
    member_state ENUM('active', 'disabled') NOT NULL DEFAULT 'active' COMMENT 'Membership state',
    display_name VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'Tenant-specific display name',
    joined_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT 'Membership creation time',
    left_at DATETIME(6) NULL DEFAULT NULL COMMENT 'Membership end time',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT 'Creation time',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT 'Last update time',

    PRIMARY KEY (tenant_user_id),
    UNIQUE KEY uk_tenant_users_tenant_user (tenant_id, user_id),
    KEY idx_tenant_users_user_state (user_id, member_state, tenant_id),
    KEY idx_tenant_users_tenant_state (tenant_id, member_state, user_id),
    CONSTRAINT fk_tenant_users_tenant
        FOREIGN KEY (tenant_id)
        REFERENCES tenants (tenant_id),
    CONSTRAINT fk_tenant_users_user
        FOREIGN KEY (user_id)
        REFERENCES users (user_id),
    CHECK (
        (member_state = 'active' AND left_at IS NULL)
        OR
        (member_state = 'disabled' AND left_at IS NOT NULL)
    )
) ENGINE=InnoDB
  DEFAULT CHARSET=utf8mb4
  COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Gosky tenant memberships';


CREATE TABLE auth_sessions (
    session_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Cryptographically random opaque session ID',
    user_id BIGINT UNSIGNED NOT NULL COMMENT 'Authenticated user ID',
    tenant_id BIGINT UNSIGNED NOT NULL COMMENT 'Verified active tenant ID',
    realm ENUM('admin', 'front') NOT NULL COMMENT 'JWT route realm',
    refresh_token_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'SHA-256 of opaque refresh token',
    refresh_generation BIGINT UNSIGNED NOT NULL DEFAULT 1 COMMENT 'Monotonic refresh rotation generation',
    issued_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT 'Session issue time',
    expires_at DATETIME(6) NOT NULL COMMENT 'Refresh expiry time',
    last_seen_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT 'Last successful authenticated request',
    revoked_at DATETIME(6) NULL DEFAULT NULL COMMENT 'Revocation time',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT 'Creation time',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT 'Last update time',

    PRIMARY KEY (session_id),
    UNIQUE KEY uk_auth_sessions_refresh_token_hash (refresh_token_hash),
    KEY idx_auth_sessions_user_tenant_realm (user_id, tenant_id, realm, expires_at),
    KEY idx_auth_sessions_expiry (expires_at, session_id),
    CONSTRAINT fk_auth_sessions_tenant_user
        FOREIGN KEY (tenant_id, user_id)
        REFERENCES tenant_users (tenant_id, user_id),
    CHECK (refresh_generation > 0),
    CHECK (expires_at > issued_at)
) ENGINE=InnoDB
  DEFAULT CHARSET=utf8mb4
  COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Gosky revocable refresh sessions';


CREATE TABLE auth_login_logs (
    auth_login_log_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Authentication audit log ID',
    user_id BIGINT UNSIGNED NULL DEFAULT NULL COMMENT 'Resolved user ID when available',
    tenant_id BIGINT UNSIGNED NULL DEFAULT NULL COMMENT 'Resolved tenant ID when available',
    realm ENUM('admin', 'front') NOT NULL COMMENT 'Attempted identity realm',
    login_name VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Submitted login name; never a credential',
    subject VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Verified subject on successful authentication',
    operation ENUM('login', 'refresh', 'logout', 'authenticate') NOT NULL COMMENT 'Authentication operation',
    outcome ENUM('allowed', 'denied', 'error') NOT NULL COMMENT 'Authentication result',
    reason_code VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'Low-cardinality denial or error reason',
    request_id VARCHAR(128) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Request correlation ID',
    trace_id VARCHAR(128) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Trace correlation ID',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT 'Audit creation time',

    PRIMARY KEY (auth_login_log_id),
    KEY idx_auth_login_logs_tenant_created (tenant_id, created_at DESC, auth_login_log_id),
    KEY idx_auth_login_logs_user_created (user_id, created_at DESC, auth_login_log_id),
    KEY idx_auth_login_logs_request_id (request_id),
    KEY idx_auth_login_logs_outcome_created (operation, outcome, created_at DESC, auth_login_log_id)
) ENGINE=InnoDB
  DEFAULT CHARSET=utf8mb4
  COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Gosky authentication audit logs';
