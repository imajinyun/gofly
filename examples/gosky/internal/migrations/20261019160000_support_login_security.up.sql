-- Login audit log and consecutive-failure lock protection for the
-- SmartAdmin support console. Logs are write-once rows emitted by the login
-- boundary; fail rows are the per-user lock state machine. The level-3
-- protect configuration is stored as one JSON value in support_configs
-- (key LEVEL3_PROTECT_CONFIG), matching the Java t_config design.
--
-- MySQL 8.0+.

CREATE TABLE support_login_logs (
    login_log_id      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Login log primary key',
    tenant_id         BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Tenant id, 0 for unresolved names',
    user_id           BIGINT UNSIGNED NULL COMMENT 'User id, NULL for unknown login names',
    user_type         TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1 admin employee',
    user_name         VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'User display name snapshot',
    login_name        VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Submitted login name',
    login_ip          VARCHAR(45) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'Client IP',
    login_ip_region   VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'IP region (reserved)',
    user_agent        VARCHAR(512) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Client user agent',
    remark            VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Remark',
    login_device      VARCHAR(64) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Login device',
    login_result      TINYINT UNSIGNED NOT NULL COMMENT '0 success 1 fail 2 logout',
    created_at        DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),

    PRIMARY KEY (login_log_id),
    KEY idx_support_login_logs_tenant_created (tenant_id, created_at),
    KEY idx_support_login_logs_user (tenant_id, user_id, created_at),
    KEY idx_support_login_logs_result (tenant_id, login_result, created_at),
    CONSTRAINT chk_support_login_logs_result CHECK (login_result IN (0, 1, 2)),
    CONSTRAINT chk_support_login_logs_user_type CHECK (user_type = 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='SmartAdmin login audit logs';

CREATE TABLE support_login_fails (
    login_fail_id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Login fail state primary key',
    tenant_id               BIGINT UNSIGNED NOT NULL COMMENT 'Tenant id',
    user_id                 BIGINT UNSIGNED NOT NULL COMMENT 'User id',
    user_type               TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1 admin employee',
    login_name              VARCHAR(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'Login name snapshot',
    login_fail_count        INT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Consecutive fail count',
    lock_flag               TINYINT(1) NOT NULL DEFAULT 0 COMMENT '1 locked 0 not locked',
    login_lock_begin_time   DATETIME(6) NULL COMMENT 'Current lock begin time',
    created_at              DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at              DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

    PRIMARY KEY (login_fail_id),
    UNIQUE KEY uk_support_login_fails_user (tenant_id, user_id, user_type),
    KEY idx_support_login_fails_lock (tenant_id, lock_flag, login_lock_begin_time),
    CONSTRAINT chk_support_login_fails_user_type CHECK (user_type = 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Consecutive login failure lock state';
