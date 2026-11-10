-- In-process scheduled jobs mirroring SmartAdmin's t_smart_job /
-- t_smart_log. Jobs are code-owned: job_class must name a registered
-- executor key, never an arbitrary type. Global tables; a single daemon
-- goroutine polls enabled rows and records every execution.
--
-- MySQL 8.0+.

CREATE TABLE support_jobs (
    job_id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Job primary key',
    job_name            VARCHAR(100) COLLATE utf8mb4_bin NOT NULL COMMENT 'Job name',
    job_class           VARCHAR(200) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Registered executor key',
    trigger_type        VARCHAR(20) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'cron | fixed_delay',
    trigger_value       VARCHAR(100) COLLATE utf8mb4_bin NOT NULL COMMENT 'Cron expression or seconds',
    param               VARCHAR(1000) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Optional job parameter',
    enabled_flag        TINYINT(1) NOT NULL DEFAULT 0 COMMENT '1 enabled 0 disabled',
    last_execute_time   DATETIME(6) NULL COMMENT 'Last execution time',
    last_execute_log_id BIGINT UNSIGNED NULL COMMENT 'Last execution log id',
    remark              VARCHAR(250) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Remark',
    sort                INT NOT NULL DEFAULT 0 COMMENT 'Sort order',
    deleted_flag        TINYINT(1) NOT NULL DEFAULT 0 COMMENT '1 deleted 0 active',
    update_name         VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Last operator',
    created_at          DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at          DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

    PRIMARY KEY (job_id),
    KEY idx_support_jobs_enabled (enabled_flag, deleted_flag, sort),
    KEY idx_support_jobs_class (job_class),
    CONSTRAINT chk_support_jobs_trigger CHECK (trigger_type IN ('cron', 'fixed_delay'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Scheduled job definitions';

CREATE TABLE support_job_logs (
    log_id               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Job log primary key',
    job_id               BIGINT UNSIGNED NOT NULL COMMENT 'Job id',
    job_name             VARCHAR(100) COLLATE utf8mb4_bin NOT NULL COMMENT 'Job name snapshot',
    param                VARCHAR(2000) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Execution parameter',
    success_flag         TINYINT(1) NOT NULL DEFAULT 1 COMMENT '1 success 0 failure',
    execute_start_time   DATETIME(6) NOT NULL COMMENT 'Execution start',
    execute_time_millis  BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Duration in milliseconds',
    execute_end_time     DATETIME(6) NOT NULL COMMENT 'Execution end',
    execute_result       VARCHAR(2000) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Result or exception',
    ip                   VARCHAR(45) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'Executor IP',
    process_id           VARCHAR(64) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Executor process id',
    program_path         VARCHAR(500) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Executor working directory',
    create_name          VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Manual trigger operator',
    created_at           DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),

    PRIMARY KEY (log_id),
    KEY idx_support_job_logs_job (job_id, created_at),
    KEY idx_support_job_logs_success (success_flag, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Scheduled job execution logs';
