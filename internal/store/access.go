package store

import (
	"context"
	"strings"
)

// Access is "whose eyes" a query is for. The zero value sees every project.
//
//	Viewer: a user ID — only the projects that user may see (see visibleProjects).
//	        "" means unrestricted: the owner, or Rexus itself (scheduler, alerts).
//	Only:   non-nil — additionally limited to these project IDs (API tokens).
//	Vault:  when true, restricts to projects where Viewer has vault access.
type Access struct {
	Viewer string
	Only   []string
	Vault  bool
}

// ForVault returns a copy of a that restricts queries to projects where the
// viewer has vault access.
func (a Access) ForVault() Access {
	a.Vault = true
	return a
}

// NoOne is a user ID that exists nowhere: an Access with this viewer sees nothing.
const NoOne = "00000000-0000-0000-0000-000000000000"

// visibleProjects is the one rule for who sees which project, as a SQL condition on a
// project ID column. It takes the viewer's user ID as its only argument (used twice).
//
//   - an explicit grant (project_access) always counts, even for an owner-only project;
//   - an admin sees every project that is not owner-only if they have "all projects",
//     and otherwise only the ones they created (also never owner-only ones).
//
// The owner is never filtered — callers skip the condition for them.
const visibleProjects = ` IN (SELECT pv.id FROM projects pv WHERE
	EXISTS (SELECT 1 FROM project_access pa WHERE pa.project_id = pv.id AND pa.user_id = ?)
	OR (NOT pv.owner_only AND EXISTS (SELECT 1 FROM users vu WHERE vu.id = ? AND vu.role = 'admin'
		AND (vu.all_projects OR pv.created_by = vu.id))))`

// vaultProjects is the one rule for who may access a project's vault secrets:
//   - an explicit grant with vault_access = true always counts, even for an owner-only project;
//   - an admin has vault access on every non-owner-only project if they have both
//     all_projects and all_vault, or on projects they created (unless owner-only).
const vaultProjects = ` IN (SELECT pv.id FROM projects pv WHERE
	EXISTS (SELECT 1 FROM project_access pa WHERE pa.project_id = pv.id AND pa.user_id = ? AND pa.vault_access)
	OR (NOT pv.owner_only AND EXISTS (SELECT 1 FROM users vu WHERE vu.id = ? AND vu.role = 'admin'
		AND ((vu.all_projects AND vu.all_vault) OR pv.created_by = vu.id))))`

// access adds the conditions that keep column col (a project ID) within a.
func (b *filterBuilder) access(col string, a Access) {
	if a.Viewer != "" {
		if a.Vault {
			b.add(col+vaultProjects, a.Viewer)
		} else {
			b.add(col+visibleProjects, a.Viewer)
		}
	}
	if a.Only != nil {
		b.add(col+" = ANY(?::uuid[])", a.Only)
	}
}

// CanSee reports whether a may see the project.
func (s *Store) CanSee(ctx context.Context, a Access, projectID string) (bool, error) {
	if !IsUUID(projectID) {
		return false, nil
	}
	var b filterBuilder
	b.add("p.id = ?", projectID)
	b.access("p.id", a)
	var ok bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM projects p`+b.sql()+`)`, b.args...).Scan(&ok)
	return ok, err
}

// CanSeeVault reports whether a has vault access to the project.
func (s *Store) CanSeeVault(ctx context.Context, a Access, projectID string) (bool, error) {
	return s.CanSee(ctx, a.ForVault(), projectID)
}

func (s *Store) ProjectByKeyFor(ctx context.Context, a Access, key string) (*Project, error) {
	var b filterBuilder
	b.add("p.key = ?", strings.ToLower(key))
	b.access("p.id", a)
	p, err := scanProject(s.Pool.QueryRow(ctx, projectSelect+b.sql(), b.args...))
	if err != nil {
		return nil, err
	}
	if a.Viewer == "" {
		p.VaultAccess = true
	} else {
		p.VaultAccess, err = s.CanSeeVault(ctx, a, p.ID)
		if err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (s *Store) ListProjectsFor(ctx context.Context, a Access) ([]*Project, error) {
	var b filterBuilder
	b.access("p.id", a)
	rows, err := s.Pool.Query(ctx, projectSelect+b.sql()+` ORDER BY p.name`, b.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if a.Viewer == "" {
		for _, p := range out {
			p.VaultAccess = true
		}
	} else if len(out) > 0 {
		vaultIDs, err := s.VisibleVaultProjectIDs(ctx, a)
		if err != nil {
			return nil, err
		}
		vset := make(map[string]bool, len(vaultIDs))
		for _, id := range vaultIDs {
			vset[id] = true
		}
		for _, p := range out {
			p.VaultAccess = vset[p.ID]
		}
	}
	return out, nil
}

// VisibleVaultProjectIDs lists the IDs of every project a has vault access to.
func (s *Store) VisibleVaultProjectIDs(ctx context.Context, a Access) ([]string, error) {
	return s.VisibleProjectIDs(ctx, a.ForVault())
}

// VisibleProjectIDs lists the IDs of every project a may see.
func (s *Store) VisibleProjectIDs(ctx context.Context, a Access) ([]string, error) {
	var b filterBuilder
	b.access("p.id", a)
	rows, err := s.Pool.Query(ctx, `SELECT p.id FROM projects p`+b.sql(), b.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ProjectIDsByKeys maps project keys to IDs; ok is false if any key is unknown.
func (s *Store) ProjectIDsByKeys(ctx context.Context, keys []string) (ids []string, ok bool, err error) {
	ids = []string{}
	seen := map[string]bool{}
	for _, k := range keys {
		k = strings.ToLower(strings.TrimSpace(k))
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		var id string
		if err := s.Pool.QueryRow(ctx, `SELECT id FROM projects WHERE key = $1`, k).Scan(&id); err != nil {
			if notFound(err) == ErrNotFound {
				return nil, false, nil
			}
			return nil, false, err
		}
		ids = append(ids, id)
	}
	return ids, true, nil
}

// ---- grants (project_access) ----

// GrantsByUser maps user ID → the keys of the projects shared with them that a can see.
func (s *Store) GrantsByUser(ctx context.Context, a Access) (map[string][]string, error) {
	var b filterBuilder
	b.access("p.id", a)
	rows, err := s.Pool.Query(ctx, `SELECT pa.user_id, p.key FROM project_access pa JOIN projects p ON p.id = pa.project_id`+
		b.sql()+` ORDER BY p.key`, b.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var uid, key string
		if err := rows.Scan(&uid, &key); err != nil {
			return nil, err
		}
		out[uid] = append(out[uid], key)
	}
	return out, rows.Err()
}

// VaultGrantsByUser maps user ID → the keys of the projects where they have explicit
// vault access, among projects that a can see.
func (s *Store) VaultGrantsByUser(ctx context.Context, a Access) (map[string][]string, error) {
	var b filterBuilder
	b.add("pa.vault_access = ?", true)
	b.access("p.id", a)
	rows, err := s.Pool.Query(ctx, `SELECT pa.user_id, p.key FROM project_access pa JOIN projects p ON p.id = pa.project_id`+
		b.sql()+` ORDER BY p.key`, b.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var uid, key string
		if err := rows.Scan(&uid, &key); err != nil {
			return nil, err
		}
		out[uid] = append(out[uid], key)
	}
	return out, rows.Err()
}

// SetUserGrants makes the user's grants, within the projects in scope, exactly
// projectIDs. Within vaultScope (the projects the actor has vault access to),
// vault_access is set to true iff the project is in vaultProjectIDs; for projects
// in scope but outside vaultScope, existing vault_access values are preserved and
// new grants default to vault_access = false.
// Grants to projects outside scope are left alone.
func (s *Store) SetUserGrants(ctx context.Context, userID string, projectIDs, vaultProjectIDs, scope, vaultScope []string, grantedBy string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM project_access WHERE user_id = $1 AND project_id = ANY($2::uuid[])
		AND NOT (project_id = ANY($3::uuid[]))`, userID, scope, projectIDs); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO project_access (user_id, project_id, vault_access, granted_by)
		SELECT $1::uuid, p, (p = ANY($4::uuid[]) AND p = ANY($5::uuid[])), $6::uuid
		FROM unnest($2::uuid[]) p WHERE p = ANY($3::uuid[])
		ON CONFLICT (user_id, project_id) DO NOTHING`, userID, projectIDs, scope, vaultProjectIDs, vaultScope, nullIfEmpty(grantedBy)); err != nil {
		return err
	}
	if len(vaultScope) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE project_access SET vault_access = (project_id = ANY($2::uuid[]))
			WHERE user_id = $1 AND project_id = ANY($3::uuid[])`, userID, vaultProjectIDs, vaultScope); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Store) SetAllProjects(ctx context.Context, userID string, v bool) error {
	_, err := s.Pool.Exec(ctx, `UPDATE users SET all_projects = $2, all_vault = (all_vault AND $2) WHERE id = $1`, userID, v)
	return err
}

func (s *Store) SetAllVault(ctx context.Context, userID string, v bool) error {
	_, err := s.Pool.Exec(ctx, `UPDATE users SET all_vault = ($2 AND all_projects) WHERE id = $1`, userID, v)
	return err
}

// ProjectPerson is someone who can see a project, and why.
type ProjectPerson struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	Via         string `json:"via"` // owner | all_projects | creator | shared
	VaultAccess bool   `json:"vault_access"`
}

// ProjectPeople lists every active user who can see the project, with the reason
// and whether they have vault access on this project.
func (s *Store) ProjectPeople(ctx context.Context, projectID string) ([]*ProjectPerson, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT u.id, u.email, u.name, u.role,
		  CASE WHEN u.role = 'owner' THEN 'owner'
		       WHEN pa.user_id IS NOT NULL THEN 'shared'
		       WHEN u.all_projects THEN 'all_projects'
		       ELSE 'creator' END,
		  (u.role = 'owner'
		   OR COALESCE(pa.vault_access, false)
		   OR (u.role = 'admin' AND NOT p.owner_only AND ((u.all_projects AND u.all_vault) OR p.created_by = u.id)))
		FROM users u
		JOIN projects p ON p.id = $1
		LEFT JOIN project_access pa ON pa.project_id = p.id AND pa.user_id = u.id
		WHERE u.status = 'active' AND (
		      u.role = 'owner'
		   OR pa.user_id IS NOT NULL
		   OR (u.role = 'admin' AND NOT p.owner_only AND (u.all_projects OR p.created_by = u.id)))
		ORDER BY CASE u.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 WHEN 'contributor' THEN 2 ELSE 3 END, lower(u.email)`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ProjectPerson{}
	for rows.Next() {
		p := &ProjectPerson{}
		if err := rows.Scan(&p.ID, &p.Email, &p.Name, &p.Role, &p.Via, &p.VaultAccess); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetProjectGrants makes the project's explicit grants, among the given users
// (the ones the actor may manage), exactly userIDs. When canManageVault is true,
// vault_access for those manageable users is set to true iff they are in vaultUserIDs;
// otherwise existing vault_access values are preserved and new grants default to false.
func (s *Store) SetProjectGrants(ctx context.Context, projectID string, userIDs, vaultUserIDs, manageable []string, canManageVault bool, grantedBy string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM project_access WHERE project_id = $1 AND user_id = ANY($2::uuid[])
		AND NOT (user_id = ANY($3::uuid[]))`, projectID, manageable, userIDs); err != nil {
		return err
	}
	if canManageVault {
		if _, err := tx.Exec(ctx, `INSERT INTO project_access (user_id, project_id, vault_access, granted_by)
			SELECT u, $1::uuid, (u = ANY($4::uuid[])), $5::uuid FROM unnest($2::uuid[]) u WHERE u = ANY($3::uuid[])
			ON CONFLICT (user_id, project_id) DO NOTHING`, projectID, userIDs, manageable, vaultUserIDs, nullIfEmpty(grantedBy)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE project_access SET vault_access = (user_id = ANY($2::uuid[]))
			WHERE project_id = $1 AND user_id = ANY($3::uuid[])`, projectID, vaultUserIDs, manageable); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(ctx, `INSERT INTO project_access (user_id, project_id, vault_access, granted_by)
			SELECT u, $1::uuid, false, $4::uuid FROM unnest($2::uuid[]) u WHERE u = ANY($3::uuid[])
			ON CONFLICT (user_id, project_id) DO NOTHING`, projectID, userIDs, manageable, nullIfEmpty(grantedBy)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ProjectExplicitVaultUsers lists the user IDs that have an explicit project_access
// row with vault_access = true for this project.
func (s *Store) ProjectExplicitVaultUsers(ctx context.Context, projectID string) (map[string]bool, error) {
	rows, err := s.Pool.Query(ctx, `SELECT user_id FROM project_access WHERE project_id = $1 AND vault_access = true`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func (s *Store) SetProjectOwnerOnly(ctx context.Context, projectID string, v bool) error {
	_, err := s.Pool.Exec(ctx, `UPDATE projects SET owner_only = $2, updated_at = now() WHERE id = $1`, projectID, v)
	return err
}

// UserGrantIDs lists every project explicitly shared with the user.
func (s *Store) UserGrantIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT project_id FROM project_access WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ProjectKeysByIDs maps project IDs to their keys.
func (s *Store) ProjectKeysByIDs(ctx context.Context, ids []string) (map[string]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, key FROM projects WHERE id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, key string
		if err := rows.Scan(&id, &key); err != nil {
			return nil, err
		}
		out[id] = key
	}
	return out, rows.Err()
}
