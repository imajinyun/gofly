-- Administrator operation audit trail mirroring SmartAdmin's t_operate_log.
-- Rows are written best-effort by the operate-log middleware after an
-- authenticated compatibility request completes; the table is append-only.
-- Request/response payloads are bounded JSON strings.
--
-- MySQL 8.0+.

CREATE TABLE support_operate_logs (
    operate_log_id     BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Operate log primary key',
    tenant_id          BIGINT UNSIGNED NOT NULL COMMENT 'Tenant id',
    operate_user_id    BIGINT UNSIGNED NULL COMMENT 'Operator user id',
    operate_user_type  TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1 admin employee',
    operate_user_name  VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Operator name snapshot',
    module             VARCHAR(120) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Operation module',
    content            VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Operation content',
    url                VARCHAR(512) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Request path',
    method             VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'HTTP method',
    param              MEDIUMTEXT NULL COMMENT 'Bounded JSON request payload',
    response           MEDIUMTEXT NULL COMMENT 'Bounded JSON response payload',
    ip                 VARCHAR(45) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'Client IP',
    ip_region          VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'IP region (reserved)',
    user_agent         VARCHAR(512) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Client user agent',
    success_flag       TINYINT(1) NOT NULL DEFAULT 1 COMMENT '1 success 0 failure',
    fail_reason        VARCHAR(1000) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Failure reason',
    created_at         DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at         DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

    PRIMARY KEY (operate_log_id),
    KEY idx_support_operate_logs_tenant_created (tenant_id, created_at),
    KEY idx_support_operate_logs_user (tenant_id, operate_user_id, created_at),
    KEY idx_support_operate_logs_success (tenant_id, success_flag, created_at),
    CONSTRAINT chk_support_operate_logs_user_type CHECK (operate_user_type = 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Administrator operation audit logs';
