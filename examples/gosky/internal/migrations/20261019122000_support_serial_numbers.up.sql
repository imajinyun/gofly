-- Business document serial numbers for the SmartAdmin /support/serialNumber
-- console. Definitions are deployment-owned seeded reference data; the
-- generator advances the last number under a row lock (no Redis dependency).
-- Records track one summary row per definition per local day.
--
-- MySQL 8.0+.

CREATE TABLE support_serial_numbers (
    serial_number_id    BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Serial definition primary key',
    business_name       VARCHAR(120) COLLATE utf8mb4_bin NOT NULL COMMENT 'Business document name',
    format              VARCHAR(200) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Pattern with [yyyy][mm][dd][nnn] placeholders',
    rule_type           VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'NONE' COMMENT 'Reset period: NONE/YEAR/MONTH/DAY',
    init_number         BIGINT NOT NULL DEFAULT 0 COMMENT 'Starting number after each period reset',
    step_random_range   INT NOT NULL DEFAULT 1 COMMENT 'Inclusive random step upper bound; 1 means sequential',
    last_number         BIGINT NULL COMMENT 'Last generated number',
    last_time           DATETIME(6) NULL COMMENT 'Last generation time',
    remark              VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '',
    system_flag         TINYINT(1) NOT NULL DEFAULT 1 COMMENT 'Seeded immutable definition when 1',
    created_at          DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at          DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

    PRIMARY KEY (serial_number_id),
    KEY idx_support_serial_numbers_rule (rule_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Business serial number definitions';

CREATE TABLE support_serial_number_records (
    id                  BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    serial_number_id    BIGINT UNSIGNED NOT NULL COMMENT 'Owning definition',
    record_date         DATE NOT NULL COMMENT 'Local generation day',
    last_number         BIGINT NOT NULL COMMENT 'Last number at the end of the day',
    last_time           DATETIME(6) NOT NULL COMMENT 'Last generation time',
    generate_count      INT NOT NULL DEFAULT 0 COMMENT 'Number of values generated that day',
    created_at          DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at          DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

    PRIMARY KEY (id),
    UNIQUE KEY uk_support_serial_records (serial_number_id, record_date),
    CONSTRAINT fk_support_serial_records_def FOREIGN KEY (serial_number_id) REFERENCES support_serial_numbers (serial_number_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Daily serial number generation summaries';
