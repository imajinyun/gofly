-- Restore the initial labels only for a rollback of this migration.
UPDATE project_members
SET member_role = CASE member_role
    WHEN 'viewer' THEN 'member'
    WHEN 'editor' THEN 'collaborator'
    ELSE member_role
END;

ALTER TABLE project_members
    MODIFY COLUMN member_role ENUM('member', 'collaborator') NOT NULL DEFAULT 'member'
        COMMENT 'Member role[member:ordinary collaborator:collaborator]';
