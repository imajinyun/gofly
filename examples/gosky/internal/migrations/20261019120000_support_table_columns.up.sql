-- Per-user table column preferences for the SmartAdmin table operator.
-- Preferences are scoped to the authenticated subject (users.subject) and a
-- numeric table id sent by the unchanged client. Only the serialized column
-- list is stored; contents are validated at the application boundary.
--
-- MySQL 8.0+.

CREATE TABLE support_table_columns (
    id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Primary key',
    subject         VARCHAR(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'Canonical user subject',
    table_id        BIGINT UNSIGNED NOT NULL COMMENT 'Client-defined table id',
    column_config   MEDIUMTEXT COLLATE utf8mb4_bin NOT NULL COMMENT 'JSON-serialized column list',
    created_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

    PRIMARY KEY (id),
    UNIQUE KEY uk_support_table_columns_subject_table (subject, table_id),
    CONSTRAINT chk_support_table_columns_config CHECK (json_valid(column_config))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Per-user table column preferences';
