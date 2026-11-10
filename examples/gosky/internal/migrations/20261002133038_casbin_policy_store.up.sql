-- Casbin's current policy snapshot. The adapter stores standard p/g rows in
-- ptype and v0..v5; domain is a validated query projection used for tenant
-- isolation, locking, and watcher fan-out.
CREATE TABLE casbin_rules (
    rule_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Casbin rule primary key',
    rule_key CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'SHA-256 of ptype and v0..v5',
    ptype VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Casbin policy type: p or g',
    domain VARCHAR(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'Derived Casbin domain: p.v1 or g.v2',
    v0 VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Casbin rule field 0',
    v1 VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Casbin rule field 1',
    v2 VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Casbin rule field 2',
    v3 VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Casbin rule field 3',
    v4 VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Casbin rule field 4',
    v5 VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Casbin rule field 5',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT 'Rule creation time',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT 'Rule update time',

    PRIMARY KEY (rule_id),
    UNIQUE KEY uk_casbin_rules_key (rule_key),
    KEY idx_casbin_rules_domain_type (domain, ptype, rule_id),
    KEY idx_casbin_rules_admin_bindings (ptype, v1, v2),

    CHECK (ptype IN ('p', 'g')),
    CHECK (
        (ptype = 'p' AND domain = v1)
        OR
        (ptype = 'g' AND domain = v2)
    )
) ENGINE=InnoDB
  DEFAULT CHARSET=utf8mb4
  COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Casbin tenant-scoped policy rules';


-- Append-only policy mutation events. Every rule mutation writes its event in
-- the same transaction, making this table both the audit record and the
-- durable outbox used by other service instances to reload policies.
CREATE TABLE casbin_policy_events (
    event_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Monotonic policy change sequence',
    change_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Cryptographically random mutation identifier',
    domain VARCHAR(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'Affected Casbin domain',
    operation ENUM('add_grouping', 'remove_grouping', 'add_policy', 'remove_policy') NOT NULL COMMENT 'Policy mutation type',
    rule_ptype VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'Changed Casbin policy type',
    rule_v0 VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Changed rule field 0',
    rule_v1 VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Changed rule field 1',
    rule_v2 VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Changed rule field 2',
    rule_v3 VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Changed rule field 3',
    rule_v4 VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Changed rule field 4',
    rule_v5 VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Changed rule field 5',
    before_rule JSON NULL COMMENT 'Rule before mutation; null for additions',
    after_rule JSON NULL COMMENT 'Rule after mutation; null for removals',
    actor_subject VARCHAR(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'Verified mutation actor',
    actor_domain VARCHAR(255) COLLATE utf8mb4_bin NOT NULL COMMENT 'Verified actor domain',
    request_id VARCHAR(128) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Request correlation identifier',
    trace_id VARCHAR(128) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Trace correlation identifier',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT 'Mutation commit time',

    PRIMARY KEY (event_id),
    UNIQUE KEY uk_casbin_policy_events_change (change_id),
    KEY idx_casbin_policy_events_domain_id (domain, event_id),
    KEY idx_casbin_policy_events_actor_created (actor_domain, actor_subject, created_at DESC, event_id)
) ENGINE=InnoDB
  DEFAULT CHARSET=utf8mb4
  COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Casbin policy mutation audit and watcher outbox';
