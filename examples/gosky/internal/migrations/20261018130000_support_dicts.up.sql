-- Data dictionary for the SmartAdmin /support/dict/** console.
-- Dictionaries and their values are deployment-owned reference data shared
-- across every tenant, so the tables are global (no tenant_id): the same
-- dict code resolves identically for all login sessions.
--
-- MySQL 8.0+.

CREATE TABLE support_dicts (
    dict_id        BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Dictionary primary key',
    dict_code      VARCHAR(100) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Unique dictionary code',
    dict_name      VARCHAR(100) COLLATE utf8mb4_bin NOT NULL COMMENT 'Display name',
    remark         VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Remark',
    disabled_flag  TINYINT(1) NOT NULL DEFAULT 0 COMMENT 'Disabled when 1',
    system_flag    TINYINT(1) NOT NULL DEFAULT 0 COMMENT 'Seeded immutable dictionary when 1',
    created_at     DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at     DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

    PRIMARY KEY (dict_id),
    UNIQUE KEY uk_support_dicts_code (dict_code),
    KEY idx_support_dicts_disabled (disabled_flag)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Global data dictionaries';

CREATE TABLE support_dict_data (
    dict_data_id    BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Dictionary value primary key',
    dict_id         BIGINT UNSIGNED NOT NULL COMMENT 'Owning dictionary',
    data_value      VARCHAR(100) COLLATE utf8mb4_bin NOT NULL COMMENT 'Stored value',
    data_label      VARCHAR(200) COLLATE utf8mb4_bin NOT NULL COMMENT 'Display label',
    data_style      VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'Optional presentation tag',
    remark          VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Remark',
    sort_order      INT NOT NULL DEFAULT 0 COMMENT 'Display sort, larger first in the unchanged UI',
    disabled_flag   TINYINT(1) NOT NULL DEFAULT 0 COMMENT 'Disabled when 1',
    created_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

    PRIMARY KEY (dict_data_id),
    UNIQUE KEY uk_support_dict_data_value (dict_id, data_value),
    KEY idx_support_dict_data_dict (dict_id, disabled_flag, sort_order),
    CONSTRAINT fk_support_dict_data_dict FOREIGN KEY (dict_id) REFERENCES support_dicts (dict_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Global data dictionary values';
