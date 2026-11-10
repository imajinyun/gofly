-- Help-document knowledge base: a tenant-scoped catalog tree, documents
-- with rich HTML bodies and upload-key attachments, menu/page relations,
-- and per-user view records. All write paths come from verified admin
-- sessions; user-side browsing only requires an authenticated subject.
--
-- MySQL 8.0+.

CREATE TABLE support_help_doc_catalogs (
    help_doc_catalog_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Catalog primary key',
    tenant_id           BIGINT UNSIGNED NOT NULL COMMENT 'Owning tenant',
    name                VARCHAR(200) COLLATE utf8mb4_bin NOT NULL COMMENT 'Catalog name',
    parent_id           BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Parent catalog, 0 for root',
    sort                INT NOT NULL DEFAULT 0 COMMENT 'Sort order',
    created_at          DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at          DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

    PRIMARY KEY (help_doc_catalog_id),
    UNIQUE KEY uk_support_help_doc_catalog_name (tenant_id, name),
    KEY idx_support_help_doc_catalog_parent (tenant_id, parent_id),
    CONSTRAINT chk_support_help_doc_catalog_name CHECK (char_length(name) BETWEEN 1 AND 200)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Help document catalogs';

CREATE TABLE support_help_docs (
    help_doc_id      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT 'Help document primary key',
    tenant_id        BIGINT UNSIGNED NOT NULL COMMENT 'Owning tenant',
    help_doc_catalog_id BIGINT UNSIGNED NOT NULL COMMENT 'Catalog id',
    title            VARCHAR(200) COLLATE utf8mb4_bin NOT NULL COMMENT 'Title',
    content_text     MEDIUMTEXT NULL COMMENT 'Plain text content',
    content_html     MEDIUMTEXT NULL COMMENT 'Sanitized HTML content',
    attachment       VARCHAR(1000) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Comma-joined upload file keys',
    sort             INT NOT NULL DEFAULT 0 COMMENT 'Sort order',
    page_view_count  INT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Total page views',
    user_view_count  INT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Distinct viewing users',
    author           VARCHAR(100) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Author snapshot',
    created_at       DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at       DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

    PRIMARY KEY (help_doc_id),
    KEY idx_support_help_docs_catalog (tenant_id, help_doc_catalog_id),
    KEY idx_support_help_docs_sort (tenant_id, sort, created_at),
    CONSTRAINT chk_support_help_docs_title CHECK (char_length(title) BETWEEN 1 AND 200)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Help documents';

CREATE TABLE support_help_doc_relations (
    id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id         BIGINT UNSIGNED NOT NULL COMMENT 'Owning tenant',
    help_doc_id       BIGINT UNSIGNED NOT NULL COMMENT 'Help document id',
    relation_id       BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Related menu/page id, 0 for home',
    relation_name     VARCHAR(200) COLLATE utf8mb4_bin NOT NULL COMMENT 'Related menu/page name',
    created_at        DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),

    PRIMARY KEY (id),
    UNIQUE KEY uk_support_help_doc_relation (tenant_id, help_doc_id, relation_id),
    KEY idx_support_help_doc_relation_rel (tenant_id, relation_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Help document menu/page relations';

CREATE TABLE support_help_doc_view_records (
    id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    tenant_id         BIGINT UNSIGNED NOT NULL COMMENT 'Owning tenant',
    help_doc_id       BIGINT UNSIGNED NOT NULL COMMENT 'Help document id',
    user_id           BIGINT UNSIGNED NOT NULL COMMENT 'Viewing user id',
    user_name         VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Viewing user snapshot',
    page_view_count   INT UNSIGNED NOT NULL DEFAULT 1 COMMENT 'View count by this user',
    first_ip          VARCHAR(45) COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'First view IP',
    first_user_agent  VARCHAR(512) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'First view user agent',
    last_ip           VARCHAR(45) COLLATE ascii_bin NOT NULL DEFAULT '' COMMENT 'Last view IP',
    last_user_agent   VARCHAR(512) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT 'Last view user agent',
    created_at        DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at        DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

    PRIMARY KEY (id),
    UNIQUE KEY uk_support_help_doc_view (tenant_id, help_doc_id, user_id),
    KEY idx_support_help_doc_view_doc (tenant_id, help_doc_id, updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='Per-user help document view records';
