-- Development-tool demo set: product change logs, process heart-beat
-- records, and the business data-tracer trail. The api-encryption demo and
-- data-masking demo are stateless and need no tables. Global tables: the
-- change log is deployment-wide, heartbeats describe processes.
--
-- MySQL 8.0+.

CREATE TABLE support_change_logs (
    change_log_id   BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Change log primary key',
    update_version  VARCHAR(100) COLLATE utf8mb4_bin NOT NULL COMMENT 'Release version',
    type            TINYINT UNSIGNED NOT NULL COMMENT '1 major 2 feature 3 bugfix',
    publish_author  VARCHAR(100) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Publisher',
    public_date     DATE NOT NULL COMMENT 'Publish date',
    content         MEDIUMTEXT NULL COMMENT 'Release notes',
    link            VARCHAR(500) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'External link',
    created_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

    PRIMARY KEY (change_log_id),
    KEY idx_support_change_logs_date (public_date),
    KEY idx_support_change_logs_type (type),
    CONSTRAINT chk_support_change_logs_type CHECK (type BETWEEN 1 AND 3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Product change logs';

CREATE TABLE support_heart_beat_records (
    heart_beat_record_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Heartbeat primary key',
    project_path         VARCHAR(500) COLLATE utf8mb4_bin NOT NULL COMMENT 'Process working directory',
    server_ip            VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Local IPv4 addresses',
    process_no           INT UNSIGNED NOT NULL COMMENT 'OS process id',
    process_start_time   DATETIME(6) NOT NULL COMMENT 'Process start time',
    heart_beat_time      DATETIME(6) NOT NULL COMMENT 'Latest heartbeat time',
    created_at           DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at           DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

    PRIMARY KEY (heart_beat_record_id),
    UNIQUE KEY uk_support_heartbeat_process (project_path(191), server_ip(64), process_no),
    KEY idx_support_heartbeat_time (heart_beat_time)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Process heartbeat records';

CREATE TABLE support_data_tracer_logs (
    data_tracer_id  BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Tracer primary key',
    data_id         BIGINT UNSIGNED NOT NULL COMMENT 'Business row id',
    type            INT NOT NULL DEFAULT 0 COMMENT 'Business module type',
    content         VARCHAR(500) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Operation summary',
    diff_old        MEDIUMTEXT NULL COMMENT 'Old field snapshot',
    diff_new        MEDIUMTEXT NULL COMMENT 'New field snapshot',
    extra_data      VARCHAR(2000) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Extension data',
    user_id         BIGINT UNSIGNED NULL COMMENT 'Operator user id',
    user_type       TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1 admin employee',
    user_name       VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Operator name',
    user_agent      VARCHAR(512) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Client user agent',
    ip              VARCHAR(45) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'Client IP',
    ip_region       VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'IP region (reserved)',
    created_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),

    PRIMARY KEY (data_tracer_id),
    KEY idx_support_tracer_data (type, data_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Business data change tracer logs';
