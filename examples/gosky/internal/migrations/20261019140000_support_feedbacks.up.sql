-- Employee feedback submitted from the side-help modal and reviewed in the
-- support console. Rows are tenant-scoped; the submitter is resolved from the
-- verified subject and feedback_attachment stores comma-joined upload keys
-- (never browser paths).
--
-- MySQL 8.0+.

CREATE TABLE support_feedbacks (
    feedback_id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Feedback primary key',
    tenant_id             BIGINT UNSIGNED NOT NULL COMMENT 'Owning tenant',
    user_id               BIGINT UNSIGNED NOT NULL COMMENT 'Submitter user id',
    user_type             TINYINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '1 admin employee',
    user_name             VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Submitter display snapshot',
    feedback_content      VARCHAR(2000) COLLATE utf8mb4_bin NOT NULL COMMENT 'Feedback content',
    feedback_attachment   VARCHAR(2048) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Comma-joined upload file keys',
    created_at            DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at            DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

    PRIMARY KEY (feedback_id),
    KEY idx_support_feedbacks_tenant_created (tenant_id, created_at),
    KEY idx_support_feedbacks_user (tenant_id, user_id),
    CONSTRAINT chk_support_feedbacks_content CHECK (char_length(feedback_content) BETWEEN 1 AND 2000),
    CONSTRAINT chk_support_feedbacks_user_type CHECK (user_type = 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Employee feedback';
