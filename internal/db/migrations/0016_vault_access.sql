-- Per-project vault access control.
--
--   owner        always has vault access on every project.
--   admin        has vault access on all non-owner-only projects if (all_projects AND all_vault),
--                on projects they created (unless owner_only), or when explicitly granted
--                vault_access = true in project_access.
--   contributor  has vault access only on projects where project_access.vault_access = true.
--   viewer       the same (sees secret metadata only when vault_access = true).
--
-- Upgrading preserves vault access for existing grants and all-project admins so nobody
-- loses access unexpectedly; new grants and new invites default to vault_access = false.

ALTER TABLE project_access ADD COLUMN vault_access boolean NOT NULL DEFAULT false;
UPDATE project_access SET vault_access = true;

ALTER TABLE users ADD COLUMN all_vault boolean NOT NULL DEFAULT false;
UPDATE users SET all_vault = true WHERE role = 'admin' AND all_projects = true;

ALTER TABLE invites ADD COLUMN all_vault boolean NOT NULL DEFAULT false;
UPDATE invites SET all_vault = true WHERE role = 'admin' AND all_projects = true AND used_at IS NULL;

ALTER TABLE invite_projects ADD COLUMN vault_access boolean NOT NULL DEFAULT false;
UPDATE invite_projects SET vault_access = true
WHERE invite_id IN (SELECT id FROM invites WHERE used_at IS NULL);
