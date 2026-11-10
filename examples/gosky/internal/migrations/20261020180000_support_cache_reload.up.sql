-- SmartReload heartbeat tables: items are code-owned reload tags polled by
-- a daemon goroutine, results are the per-execution audit trail. Global
-- tables (no tenant_id): reload hooks operate on process-wide state.
--
-- MySQL 8.0+.

CREATE TABLE support_reload_items (
    tag             VARCHAR(120) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Reload tag (code-owned key)',
    args            VARCHAR(1000) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Last reload arguments',
    identification  VARCHAR(120) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Changed-state identifier',
    created_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

    PRIMARY KEY (tag)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='SmartReload heartbeat items';

CREATE TABLE support_reload_results (
    result_id       BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Result primary key',
    tag             VARCHAR(120) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Reload tag',
    identification  VARCHAR(120) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Identification at execution',
    args            VARCHAR(1000) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Reload arguments',
    result_flag     TINYINT(1) NOT NULL DEFAULT 1 COMMENT '1 success 0 failure',
    exception       VARCHAR(2000) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Failure exception text',
    created_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),

    PRIMARY KEY (result_id),
    KEY idx_support_reload_results_tag (tag, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='SmartReload execution results';

-- Code-owned reload tags, mirroring ReloadConst in the Java reference.
INSERT INTO support_reload_items (tag, args, identification) VALUES
    ('cache_service', '', 'init'),
    ('system_config', '', 'init');
