package server

import (
	"net/http"

	"rexus/internal/store"
)

// accessUser is one person in a project's sharing list.
type accessUser struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	Shared      bool   `json:"shared"`       // has an explicit grant to this project
	VaultAccess bool   `json:"vault_access"` // has explicit vault access on this project
	Manageable  bool   `json:"manageable"`   // the caller may add or remove that grant
}

// manageableForGrants says whose grants the caller may change: contributors and
// viewers, and — for the owner only — admins who see only chosen projects.
func manageableForGrants(actorRole string, u *store.User) bool {
	if u.Status != "active" || u.Role == "owner" {
		return false
	}
	switch u.Role {
	case "contributor", "viewer":
		return actorRole == "owner" || actorRole == "admin"
	case "admin":
		return actorRole == "owner"
	}
	return false
}

// handleGetProjectAccess: who can see this project, and whom it can be shared with.
func (s *Server) handleGetProjectAccess(w http.ResponseWriter, r *http.Request, a *authCtx) {
	if !requireAdmin(w, a) {
		return
	}
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	people, err := s.store.ProjectPeople(r.Context(), p.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	explicitVault, err := s.store.ProjectExplicitVaultUsers(r.Context(), p.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	shared := map[string]bool{}
	for _, pe := range people {
		if pe.Via == "shared" {
			shared[pe.ID] = true
		}
	}
	list := []accessUser{}
	for _, u := range users {
		if u.Role == "owner" || u.Status != "active" {
			continue
		}
		list = append(list, accessUser{ID: u.ID, Email: u.Email, Name: u.Name, Role: u.Role,
			Shared: shared[u.ID], VaultAccess: explicitVault[u.ID], Manageable: manageableForGrants(a.user.Role, u)})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"owner_only":         p.OwnerOnly,
		"can_set_owner_only": a.user.Role == "owner",
		"can_manage_vault":   p.VaultAccess,
		"people":             people,
		"users":              list,
	})
}

// handleSetProjectAccess shares the project with exactly these users (among the ones
// the caller may manage); everyone else's grants are left as they are.
func (s *Server) handleSetProjectAccess(w http.ResponseWriter, r *http.Request, a *authCtx) {
	if !requireAdmin(w, a) {
		return
	}
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	var in struct {
		UserIDs      []string  `json:"user_ids"`
		VaultUserIDs *[]string `json:"vault_user_ids"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.VaultUserIDs != nil && !p.VaultAccess {
		writeError(w, http.StatusForbidden, "you don't have vault access to this project")
		return
	}
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	manageable := map[string]*store.User{}
	manageableIDs := []string{}
	for _, u := range users {
		if manageableForGrants(a.user.Role, u) {
			manageable[u.ID] = u
			manageableIDs = append(manageableIDs, u.ID)
		}
	}
	chosen := []string{}
	emails := []string{}
	seen := map[string]bool{}
	for _, id := range in.UserIDs {
		u := manageable[id]
		if u == nil {
			writeError(w, http.StatusBadRequest, "you can't change access for one of those people")
			return
		}
		if !seen[id] {
			seen[id] = true
			chosen = append(chosen, id)
			emails = append(emails, u.Email)
		}
	}
	vaultChosen := []string{}
	vaultEmails := []string{}
	canManageVault := in.VaultUserIDs != nil && p.VaultAccess
	if canManageVault {
		vseen := map[string]bool{}
		for _, id := range *in.VaultUserIDs {
			u := manageable[id]
			if u == nil {
				writeError(w, http.StatusBadRequest, "you can't change vault access for one of those people")
				return
			}
			if !seen[id] {
				writeError(w, http.StatusBadRequest, "vault access requires project access")
				return
			}
			if !vseen[id] {
				vseen[id] = true
				vaultChosen = append(vaultChosen, id)
				vaultEmails = append(vaultEmails, u.Email)
			}
		}
	}
	if err := s.store.SetProjectGrants(r.Context(), p.ID, chosen, vaultChosen, manageableIDs, canManageVault, a.user.ID); err != nil {
		s.internalError(w, r, err)
		return
	}
	details := map[string]any{"project": p.Key, "shared_with": emails}
	if canManageVault {
		details["vault_with"] = vaultEmails
	}
	s.audit(r, a, "project.access", "project", p.ID, details)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
