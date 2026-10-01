-- Project authorization storage for gosky.
-- Apply with the deployment-approved migration runner before setting
-- project_store_enabled=true.
-- The service never applies this migration itself.
--
-- Assumption:
-- - MySQL 8.0+
-- - deleted projects allow name reuse within the same tenant.
-- - archived projects still occupy project_name.

CREATE TABLE projects (
    project_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '项目 ID，自增主键',
    tenant_id BIGINT UNSIGNED NOT NULL COMMENT '项目所属租户 ID',
    owner_subject VARCHAR(255) COLLATE utf8mb4_bin NOT NULL COMMENT '项目 Owner 主体标识',
    project_name VARCHAR(255) NOT NULL COMMENT '项目名称',
    project_memo TEXT NOT NULL COMMENT '项目备注',
    project_state ENUM('active', 'archived', 'deleted') NOT NULL DEFAULT 'active' COMMENT '项目状态[active:有效 archived:已归档 deleted:已删除]',
    created_by VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT '创建人主体标识',
    updated_by VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT '最后更新人主体标识',
    deleted_at DATETIME(6) NULL DEFAULT NULL COMMENT '软删除时间',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '更新时间',

    -- 仅用于唯一约束：deleted 项目释放名称；active/archived 仍占用名称
    active_project_name VARCHAR(255)
        GENERATED ALWAYS AS (
            CASE
                WHEN project_state = 'deleted' THEN NULL
                ELSE project_name
            END
        ) STORED COMMENT '未删除项目名称，用于唯一约束',

    PRIMARY KEY (project_id),

    -- 支持 project_members 用 (project_id, project_tenant_id) 做复合外键，保证租户冗余字段一致
    UNIQUE KEY uk_projects_id_tenant (
        project_id,
        tenant_id
    ),

    UNIQUE KEY uk_projects_tenant_active_name (
        tenant_id,
        active_project_name
    ),

    KEY idx_projects_tenant_owner_state_id (
        tenant_id,
        owner_subject,
        project_state,
        project_id
    ),

    KEY idx_projects_tenant_state_id (
        tenant_id,
        project_state,
        project_id
    ),

    KEY idx_projects_tenant_updated (
        tenant_id,
        updated_at DESC,
        project_id
    ),

    CHECK (
        (project_state = 'deleted' AND deleted_at IS NOT NULL)
        OR
        (project_state <> 'deleted')
    )
) ENGINE=InnoDB
  DEFAULT CHARSET=utf8mb4
  COLLATE=utf8mb4_0900_ai_ci
  COMMENT='项目授权存储表';


CREATE TABLE project_members (
    project_member_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '项目成员关系 ID，自增主键',
    project_id BIGINT UNSIGNED NOT NULL COMMENT '项目 ID，关联 projects.project_id',
    project_tenant_id BIGINT UNSIGNED NOT NULL COMMENT '项目所属租户 ID，冗余字段，用于鉴权查询',
    member_subject VARCHAR(255) COLLATE utf8mb4_bin NOT NULL COMMENT '成员主体标识',
    member_role ENUM('member', 'collaborator') NOT NULL DEFAULT 'member' COMMENT '成员角色[member:普通成员 collaborator:协作者]',
    member_state ENUM('active', 'revoked') NOT NULL DEFAULT 'active' COMMENT '成员状态[active:有效 revoked:已撤销]',
    member_memo VARCHAR(512) NOT NULL DEFAULT '' COMMENT '成员授权备注',
    granted_by VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT '授权人主体标识',
    revoked_by VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT '撤销人主体标识',
    revoked_at DATETIME(6) NULL DEFAULT NULL COMMENT '撤销时间',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '更新时间',

    PRIMARY KEY (project_member_id),

    -- 代表“一个项目内一个主体只有一条当前关系记录”。
    -- 撤销后如再次授权，建议 UPDATE 原记录：member_state='active', revoked_at=NULL。
    UNIQUE KEY uk_project_members_project_subject (
        project_id,
        member_subject
    ),

    KEY idx_project_members_subject_state_project (
        member_subject,
        member_state,
        project_id
    ),

    KEY idx_project_members_project_state_role (
        project_id,
        member_state,
        member_role
    ),

    KEY idx_project_members_tenant_subject_state (
        project_tenant_id,
        member_subject,
        member_state,
        project_id
    ),

    KEY idx_project_members_project_updated (
        project_id,
        updated_at DESC,
        project_member_id
    ),

    CONSTRAINT fk_project_members_project
        FOREIGN KEY (
            project_id,
            project_tenant_id
        )
        REFERENCES projects (
            project_id,
            tenant_id
        )
        ON DELETE CASCADE,

    CHECK (
        (member_state = 'revoked' AND revoked_at IS NOT NULL)
        OR
        (member_state = 'active' AND revoked_at IS NULL)
    )
) ENGINE=InnoDB
  DEFAULT CHARSET=utf8mb4
  COLLATE=utf8mb4_0900_ai_ci
  COMMENT='项目成员授权表';


CREATE TABLE opslogs (
    opslog_id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '操作日志 ID，自增主键',
    operator_tenant_id BIGINT UNSIGNED NOT NULL COMMENT '操作者所属租户 ID',
    operator_subject VARCHAR(255) COLLATE utf8mb4_bin NOT NULL COMMENT '操作者主体标识',
    project_tenant_id BIGINT UNSIGNED NOT NULL COMMENT '项目所属租户 ID',
    project_id BIGINT UNSIGNED NOT NULL COMMENT '项目 ID；不加外键，用于保留已删除项目的历史日志',
    target_subject VARCHAR(255) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT '被操作主体标识，例如被添加或移除的成员',
    target_role VARCHAR(64) NOT NULL DEFAULT '' COMMENT '被操作角色，例如 member、collaborator',
    operation VARCHAR(64) NOT NULL COMMENT '操作类型',
    outcome ENUM('allowed', 'denied', 'not_found', 'error') NOT NULL COMMENT '授权结果[allowed:允许 denied:拒绝 not_found:未找到 error:异常]',
    auth_basis VARCHAR(64) NOT NULL COMMENT '授权依据，例如 owner、member、collaborator 等',
    reason VARCHAR(512) NOT NULL DEFAULT '' COMMENT '授权判断说明',
    error_code VARCHAR(64) NOT NULL DEFAULT '' COMMENT '错误码',
    error_body VARCHAR(512) NOT NULL DEFAULT '' COMMENT '错误信息',
    request_id VARCHAR(128) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT '请求 ID，用于业务请求追踪',
    trace_id VARCHAR(128) COLLATE utf8mb4_bin NOT NULL DEFAULT '' COMMENT '链路追踪 ID',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '日志创建时间',

    PRIMARY KEY (opslog_id),

    KEY idx_opslogs_project_created (
        project_id,
        created_at DESC,
        opslog_id
    ),

    KEY idx_opslogs_operator_created (
        operator_tenant_id,
        operator_subject,
        created_at DESC,
        opslog_id
    ),

    KEY idx_opslogs_target_created (
        target_subject,
        created_at DESC,
        opslog_id
    ),

    KEY idx_opslogs_request_id (
        request_id
    ),

    KEY idx_opslogs_trace_id (
        trace_id
    ),

    KEY idx_opslogs_tenant_created (
        project_tenant_id,
        created_at DESC,
        opslog_id
    ),

    KEY idx_opslogs_operation_outcome_created (
        operation,
        outcome,
        created_at DESC
    )
) ENGINE=InnoDB
  DEFAULT CHARSET=utf8mb4
  COLLATE=utf8mb4_0900_ai_ci
  COMMENT='项目授权操作日志表';
