-- In-app messages for the SmartAdmin console: admins send messages to
-- employees, recipients read their own inbox and mark messages read.
-- Messages are scoped by canonical users.subject; receivers can never
-- address or read another subject's inbox.
--
-- MySQL 8.0+.

CREATE TABLE support_messages (
    message_id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Message primary key',
    sender_subject        VARCHAR(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'Canonical sender subject',
    receiver_subject      VARCHAR(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'Canonical receiver subject',
    title                 VARCHAR(200) COLLATE utf8mb4_bin NOT NULL COMMENT 'Message title',
    content               VARCHAR(2000) COLLATE utf8mb4_bin NOT NULL COMMENT 'Message content',
    message_type          TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1 mail, 2 order',
    read_flag             TINYINT(1) NOT NULL DEFAULT 0 COMMENT 'Read when 1',
    read_time             DATETIME(6) NULL COMMENT 'Marked-read time',
    created_at            DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at            DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

    PRIMARY KEY (message_id),
    KEY idx_support_messages_receiver (receiver_subject, read_flag, created_at),
    KEY idx_support_messages_sender (sender_subject, created_at),
    CONSTRAINT chk_support_messages_type CHECK (message_type BETWEEN 1 AND 2),
    CONSTRAINT chk_support_messages_title CHECK (char_length(title) BETWEEN 1 AND 200),
    CONSTRAINT chk_support_messages_content CHECK (char_length(content) BETWEEN 1 AND 2000)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='In-app messages';
