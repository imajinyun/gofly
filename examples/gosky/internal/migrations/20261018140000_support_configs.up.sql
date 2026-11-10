-- Tenant-independent runtime configuration parameters for the SmartAdmin
-- support/config console. Global table (no tenant_id): parameters are
-- deployment-owned reference data, protected by support.config capabilities.
--
-- MySQL 8.0+.

CREATE TABLE support_configs (
    config_id     BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Config primary key',
    config_key    VARCHAR(120) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Unique parameter key',
    config_name   VARCHAR(120) COLLATE utf8mb4_bin NOT NULL COMMENT 'Display name',
    config_value  VARCHAR(2000) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Parameter value',
    remark        VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Remark',
    system_flag   TINYINT(1) NOT NULL DEFAULT 0 COMMENT 'Seeded immutable parameter when 1',
    created_at    DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at    DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

    PRIMARY KEY (config_id),
    UNIQUE KEY uk_support_configs_key (config_key),
    KEY idx_support_configs_name (config_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Global runtime configuration parameters';
