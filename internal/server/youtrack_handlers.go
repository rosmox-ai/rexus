package server

import (
	"net/http"
	"strings"

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
	if r.URL.Query().Get("sync") == "1" {
		s.alerts.RefreshOpenLinks(r.Context())
	}
	projID := ""
	if pKey := strings.TrimSpace(r.URL.Query().Get("project")); pKey != "" {
		p, err := s.store.ProjectByKeyFor(r.Context(), s.access(r), pKey)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"links": []*store.YouTrackLink{}})
			return
		}
		projID = p.ID
	}
	onlyUnresolved := r.URL.Query().Get("unresolved") != "0"
	links, err := s.store.ListYouTrackLinks(r.Context(), s.access(r), projID, onlyUnresolved, 200)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": links})
}

func (s *Server) handleSyncYouTrackLinks(w http.ResponseWriter, r *http.Request, a *authCtx) {
	s.alerts.RefreshOpenLinks(r.Context())
	links, err := s.store.ListYouTrackLinks(r.Context(), s.access(r), "", false, 200)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": links})
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
