package server

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"rexus/internal/alerts"
	"rexus/internal/store"
)

func (s *Server) handleYouTrackStatus(w http.ResponseWriter, r *http.Request, a *authCtx) {
	cfg, _, err := s.alerts.ActiveYouTrackConfig(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"configured":      false,
			"base_url":        "",
			"default_project": "",
			"projects":        []alerts.YTProject{},
		})
		return
	}
	projs, _, perr := s.alerts.ListYouTrackProjects(r.Context())
	if projs == nil {
		projs = []alerts.YTProject{}
	}
	res := map[string]any{
		"configured":      true,
		"base_url":        cfg.YouTrackURL,
		"default_project": cfg.YouTrackProj,
		"projects":        projs,
	}
	if perr != nil {
		res["error"] = perr.Error()
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleListYouTrackLinks(w http.ResponseWriter, r *http.Request, a *authCtx) {
	projID := ""
	pKey := strings.TrimSpace(r.URL.Query().Get("project"))
	if pKey != "" {
		p, err := s.store.ProjectByKeyFor(r.Context(), s.access(r), pKey)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"links": []*store.YouTrackLink{}})
			return
		}
		projID = p.ID
	}
	onlyUnresolved := r.URL.Query().Get("unresolved") != "0"
	includeYT := r.URL.Query().Get("include_youtrack") == "1"

	var liveIssues []alerts.YTIssue
	if includeYT {
		liveIssues, _ = s.alerts.FetchAllIssues(r.Context())
	} else if r.URL.Query().Get("sync") == "1" {
		s.alerts.RefreshOpenLinks(r.Context())
	}

	links, err := s.store.ListYouTrackLinks(r.Context(), s.access(r), projID, onlyUnresolved, 200)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if includeYT && len(liveIssues) > 0 {
		links = s.mergeLiveYouTrackIssues(r.Context(), a, s.access(r), links, liveIssues, pKey, onlyUnresolved)
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": links})
}

func (s *Server) handleSyncYouTrackLinks(w http.ResponseWriter, r *http.Request, a *authCtx) {
	liveIssues, _ := s.alerts.FetchAllIssues(r.Context())
	links, err := s.store.ListYouTrackLinks(r.Context(), s.access(r), "", false, 200)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if len(liveIssues) > 0 {
		links = s.mergeLiveYouTrackIssues(r.Context(), a, s.access(r), links, liveIssues, "", false)
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": links})
}

func (s *Server) mergeLiveYouTrackIssues(ctx context.Context, a *authCtx, access store.Access, links []*store.YouTrackLink, liveIssues []alerts.YTIssue, filterProjKey string, onlyUnresolved bool) []*store.YouTrackLink {
	seen := make(map[string]bool, len(links)*2)
	for _, l := range links {
		seen[l.IssueID] = true
		seen[l.IssueReadable] = true
	}

	projects, _ := s.store.ListProjectsFor(ctx, access)
	matchProject := func(ytShort, ytName string) *store.Project {
		for _, p := range projects {
			if p.YouTrackProjectKey != nil && strings.EqualFold(*p.YouTrackProjectKey, ytShort) {
				return p
			}
		}
		for _, p := range projects {
			if strings.EqualFold(p.Key, ytShort) || strings.EqualFold(p.Name, ytName) || strings.EqualFold(p.Key, ytName) {
				return p
			}
		}
		return nil
	}

	isAdminUser := a != nil && (a.user.Role == "owner" || a.user.Role == "admin")
	for _, iss := range liveIssues {
		if seen[iss.ID] || seen[iss.IDReadable] {
			continue
		}
		resolved := iss.Resolved != nil
		if onlyUnresolved && resolved {
			continue
		}
		ytShort := iss.ProjectShortName
		if ytShort == "" {
			if idx := strings.IndexByte(iss.IDReadable, '-'); idx > 0 {
				ytShort = iss.IDReadable[:idx]
			}
		}
		p := matchProject(ytShort, iss.ProjectName)
		if p == nil && !isAdminUser {
			// Non-admin users only see YouTrack tickets mapped to projects they have access to.
			continue
		}
		projID, projKey, projName := "", "", iss.ProjectName
		if p != nil {
			projID, projKey, projName = p.ID, p.Key, p.Name
		}
		if filterProjKey != "" && !strings.EqualFold(projKey, filterProjKey) {
			continue
		}
		var prio, ass *string
		if iss.Priority != "" {
			pr := iss.Priority
			prio = &pr
		}
		if iss.Assignee != "" {
			as := iss.Assignee
			ass = &as
		}
		created := time.Now()
		if iss.Created > 0 {
			created = time.UnixMilli(iss.Created)
		}
		updated := created
		if iss.Updated > 0 {
			updated = time.UnixMilli(iss.Updated)
		}
		links = append(links, &store.YouTrackLink{
			ID:            "yt:" + iss.ID,
			ProjectID:     projID,
			ProjectKey:    projKey,
			ProjectName:   projName,
			ResourceType:  "youtrack",
			YTProjectKey:  ytShort,
			IssueID:       iss.ID,
			IssueReadable: iss.IDReadable,
			IssueURL:      iss.URL,
			Summary:       iss.Summary,
			State:         iss.State,
			Priority:      prio,
			Assignee:      ass,
			Resolved:      resolved,
			CreatedAt:     created,
			UpdatedAt:     updated,
		})
	}

	sort.SliceStable(links, func(i, j int) bool {
		if links[i].Resolved != links[j].Resolved {
			return !links[i].Resolved
		}
		return links[i].UpdatedAt.After(links[j].UpdatedAt)
	})
	return links
}

func (s *Server) handleProjectYouTrackIssues(w http.ResponseWriter, r *http.Request, a *authCtx) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	cfg, _, err := s.alerts.ActiveYouTrackConfig(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"configured":     false,
			"base_url":       "",
			"yt_project_key": "",
			"issues":         []alerts.YTIssue{},
			"links":          []*store.YouTrackLink{},
		})
		return
	}
	ytKey := ""
	if p.YouTrackProjectKey != nil && *p.YouTrackProjectKey != "" {
		ytKey = *p.YouTrackProjectKey
	} else if cfg.YouTrackProj != "" {
		ytKey = cfg.YouTrackProj
	}
	issues, ierr := s.alerts.FetchProjectIssues(r.Context(), p.ID, ytKey)
	if issues == nil {
		issues = []alerts.YTIssue{}
	}
	links, _ := s.store.ListYouTrackLinks(r.Context(), s.access(r), p.ID, false, 100)
	if links == nil {
		links = []*store.YouTrackLink{}
	}
	res := map[string]any{
		"configured":     true,
		"base_url":       cfg.YouTrackURL,
		"yt_project_key": ytKey,
		"issues":         issues,
		"links":          links,
	}
	if ierr != nil {
		res["error"] = ierr.Error()
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleCreateYouTrackIssue(w http.ResponseWriter, r *http.Request, a *authCtx) {
	if !requireWrite(w, a) {
		return
	}
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	var in struct {
		YTProjectKey string  `json:"yt_project_key"`
		Summary      string  `json:"summary"`
		Description  string  `json:"description"`
		Priority     string  `json:"priority"`
		Assignee     string  `json:"assignee"`
		DueDate      string  `json:"due_date"`
		ResourceType string  `json:"resource_type"`
		ResourceID   *string `json:"resource_id"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	summary := strings.TrimSpace(in.Summary)
	if summary == "" {
		writeError(w, http.StatusBadRequest, "summary is required")
		return
	}
	ytKey := strings.TrimSpace(in.YTProjectKey)
	if ytKey == "" && p.YouTrackProjectKey != nil {
		ytKey = *p.YouTrackProjectKey
	}
	if ytKey == "" {
		if cfg, _, err := s.alerts.ActiveYouTrackConfig(r.Context()); err == nil {
			ytKey = cfg.YouTrackProj
		}
	}
	link, issue, err := s.alerts.CreateManualIssue(r.Context(), alerts.YTIssueCreateInput{
		ProjectID:    p.ID,
		YTProjectKey: ytKey,
		Summary:      summary,
		Description:  strings.TrimSpace(in.Description),
		Priority:     strings.TrimSpace(in.Priority),
		Assignee:     strings.TrimSpace(in.Assignee),
		DueDate:      strings.TrimSpace(in.DueDate),
		ResourceType: strings.TrimSpace(in.ResourceType),
		ResourceID:   in.ResourceID,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, a, "youtrack.issue_create", "youtrack_issue", issue.IDReadable, map[string]any{
		"project":       p.Key,
		"yt_project":    ytKey,
		"issue":         issue.IDReadable,
		"summary":       issue.Summary,
		"resource_type": in.ResourceType,
	})
	writeJSON(w, http.StatusCreated, map[string]any{
		"link":  link,
		"issue": issue,
	})
}

func (s *Server) handleSetYouTrackIssueState(w http.ResponseWriter, r *http.Request, a *authCtx) {
	if !requireWrite(w, a) {
		return
	}
	issueID := strings.TrimSpace(r.PathValue("id"))
	if issueID == "" {
		writeError(w, http.StatusBadRequest, "issue id is required")
		return
	}
	var in struct {
		Resolved bool `json:"resolved"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	issue, err := s.alerts.SetIssueResolved(r.Context(), issueID, in.Resolved)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	action := "youtrack.issue_reopen"
	if in.Resolved {
		action = "youtrack.issue_resolve"
	}
	s.audit(r, a, action, "youtrack_issue", issue.IDReadable, map[string]any{
		"issue":    issue.IDReadable,
		"state":    issue.State,
		"resolved": in.Resolved,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"issue":    issue,
		"state":    issue.State,
		"resolved": issue.Resolved != nil || in.Resolved,
	})
}
