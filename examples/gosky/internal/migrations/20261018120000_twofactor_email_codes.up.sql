-- Email-code second factor for the SmartAdmin login flow.
-- Codes are stored only as HMAC hashes (never plaintext); attempts are
-- kept in a separate table for rate limiting. Tables are pre-auth, keyed by
-- normalized (lowercase trimmed) login name rather than tenant.
--
-- MySQL 8.0+.

CREATE TABLE twofactor_login_codes (
    login_name   VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Normalized login name',
    code_hash    VARCHAR(128) COLLATE utf8mb4_bin NOT NULL COMMENT 'HMAC of code bound to login/issuer/expiry',
    expires_at   DATETIME(6) NOT NULL COMMENT 'Code expiration time',
    created_at   DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at   DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

    PRIMARY KEY (login_name),
    KEY idx_twofactor_codes_expires (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Pre-auth email 2FA codes (hashed)';

CREATE TABLE twofactor_login_attempts (
    attempt_id     BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    login_name     VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Normalized login name',
    attempted_at   DATETIME(6) NOT NULL COMMENT 'Wrong-code attempt time',
    created_at     DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),

    PRIMARY KEY (attempt_id),
    KEY idx_twofactor_attempts_name_time (login_name, attempted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Pre-auth email 2FA wrong-code attempts';
