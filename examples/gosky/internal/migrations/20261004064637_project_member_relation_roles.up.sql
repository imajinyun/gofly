-- Replace the ambiguous initial member/collaborator labels with the explicit
-- Project relation model used by the authorization service. Existing active
-- and revoked rows retain their state and audit fields.
UPDATE project_members
SET member_role = CASE member_role
    WHEN 'member' THEN 'viewer'
    WHEN 'collaborator' THEN 'editor'
    ELSE member_role
END;

ALTER TABLE project_members
    MODIFY COLUMN member_role ENUM('viewer', 'editor') NOT NULL DEFAULT 'viewer'
        COMMENT 'Project relation role[viewer:read editor:read/write]';
