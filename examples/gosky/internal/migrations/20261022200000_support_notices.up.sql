-- OA notice board: notice types, notices, per-employee visibility
-- scopes, and per-employee view records. Mirrors the Java t_notice*
-- family. Global support tables; every tenant employee shares the board.
--
-- MySQL 8.0+.

CREATE TABLE support_notice_types (
    notice_type_id   BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Notice type primary key',
    notice_type_name VARCHAR(100) COLLATE utf8mb4_bin NOT NULL COMMENT 'Type name',
    created_at       DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at       DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

    PRIMARY KEY (notice_type_id),
    UNIQUE KEY uk_support_notice_type_name (notice_type_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Notice categories';

CREATE TABLE support_notices (
    notice_id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Notice primary key',
    notice_type_id        BIGINT UNSIGNED NOT NULL COMMENT 'Notice type',
    title                 VARCHAR(200) COLLATE utf8mb4_bin NOT NULL COMMENT 'Title',
    all_visible_flag      TINYINT(1) NOT NULL DEFAULT 1 COMMENT 'Visible to every employee',
    scheduled_publish_flag TINYINT(1) NOT NULL DEFAULT 0 COMMENT 'Publish at publish_time',
    publish_time          DATETIME(6) NOT NULL COMMENT 'Scheduled/immediate publish time',
    content_text          MEDIUMTEXT NULL COMMENT 'Plain text content',
    content_html          MEDIUMTEXT NULL COMMENT 'HTML content',
    attachment            VARCHAR(1000) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Comma-joined file keys',
    page_view_count       INT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Page views',
    user_view_count       INT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Distinct user views',
    author                VARCHAR(100) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Author',
    source                VARCHAR(100) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Source',
    document_number       VARCHAR(100) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Document number',
    deleted_flag          TINYINT(1) NOT NULL DEFAULT 0 COMMENT '1 deleted 0 active',
    create_user_id        BIGINT UNSIGNED NULL COMMENT 'Creator user id',
    create_user_name      VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Creator name snapshot',
    created_at            DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at            DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

    PRIMARY KEY (notice_id),
    KEY idx_support_notices_type (notice_type_id),
    KEY idx_support_notices_publish (publish_time),
    KEY idx_support_notices_deleted (deleted_flag),
    CONSTRAINT chk_support_notices_type CHECK (notice_type_id > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='OA notices';

CREATE TABLE support_notice_visible_ranges (
    range_id    BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Range primary key',
    notice_id   BIGINT UNSIGNED NOT NULL COMMENT 'Notice id',
    data_type   TINYINT UNSIGNED NOT NULL COMMENT '1 employee 2 department',
    data_id     BIGINT UNSIGNED NOT NULL COMMENT 'Employee or department id',
    created_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),

    PRIMARY KEY (range_id),
    UNIQUE KEY uk_support_notice_range (notice_id, data_type, data_id),
    KEY idx_support_notice_range_data (data_type, data_id),
    CONSTRAINT chk_support_notice_range_type CHECK (data_type IN (1, 2))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Notice per-employee/department visibility scopes';

CREATE TABLE support_notice_view_records (
    view_id        BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'View record primary key',
    notice_id      BIGINT UNSIGNED NOT NULL COMMENT 'Notice id',
    user_id        BIGINT UNSIGNED NOT NULL COMMENT 'Viewer user id',
    page_view_count INT UNSIGNED NOT NULL DEFAULT 1 COMMENT 'View count by this user',
    first_ip       VARCHAR(45) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'First IP',
    first_user_agent VARCHAR(512) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'First user agent',
    last_ip        VARCHAR(45) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'Last IP',
    last_user_agent VARCHAR(512) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Last user agent',
    created_at     DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT 'First view',
    updated_at     DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT 'Last view',

    PRIMARY KEY (view_id),
    UNIQUE KEY uk_support_notice_view (notice_id, user_id),
    KEY idx_support_notice_view_user (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Per-user notice view records';

-- Default type set so the console has categories on a fresh database.
INSERT IGNORE INTO support_notice_types (notice_type_id, notice_type_name) VALUES
    (1, '通知'),
    (2, '公告');
