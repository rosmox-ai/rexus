package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"rexus/internal/store"
)

// YTProject describes a YouTrack project returned from /api/admin/projects.
type YTProject struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ShortName string `json:"shortName"`
	Archived  bool   `json:"archived"`
}

// YTIssue describes a YouTrack issue returned from /api/issues.
type YTIssue struct {
	ID         string `json:"id"`
	IDReadable string `json:"idReadable"`
	Summary    string `json:"summary"`
	Resolved   *int64 `json:"resolved"`
	Created    int64  `json:"created"`
	Updated    int64  `json:"updated"`
	URL        string `json:"url"`
	State      string `json:"state"`
	Priority   string `json:"priority"`
	Assignee   string `json:"assignee"`
	Type       string `json:"type"`
}

// YTIssueCreateInput holds inputs for manually creating a YouTrack issue from Rexus.
type YTIssueCreateInput struct {
	ProjectID    string  `json:"project_id"`
	YTProjectKey string  `json:"yt_project_key"`
	Summary      string  `json:"summary"`
	Description  string  `json:"description"`
	Priority     string  `json:"priority"`
	Assignee     string  `json:"assignee"`
	DueDate      string  `json:"due_date"`      // YYYY-MM-DD
	ResourceType string  `json:"resource_type"` // check | job | secret | ssl | manual
	ResourceID   *string `json:"resource_id"`
}

// SeedYouTrack ensures that if REXUS_YOUTRACK_URL and REXUS_YOUTRACK_TOKEN are set in .env,
// a YouTrack alert channel exists in the database so alerts and UI features work out of the box.
func (s *Service) SeedYouTrack(ctx context.Context, ytURL, ytToken string) {
	ytURL = strings.TrimRight(strings.TrimSpace(ytURL), "/")
	ytToken = strings.TrimSpace(ytToken)
	if ytURL == "" || ytToken == "" {
		return
	}
	channels, err := s.store.ListAlertChannels(ctx, false)
	if err != nil {
		s.log.Warn("youtrack: list channels for seed", "err", err)
		return
	}
	for _, ch := range channels {
		if ch.Type == TypeYouTrack {
			// Already exists; if its URL or token came from .env and changed, update it if needed.
			cfg, err := s.DecryptConfig(ch.ConfigEnc)
			if err == nil && cfg.YouTrackURL == ytURL && cfg.YouTrackToken == ytToken {
				return
			}
			if cfg == nil {
				cfg = &Config{}
			}
			cfg.YouTrackURL = ytURL
			cfg.YouTrackToken = ytToken
			if cfg.AutoResolve == nil {
				tr := true
				cfg.AutoResolve = &tr
			}
			if enc, err := s.EncryptConfig(cfg); err == nil {
				_, _ = s.store.UpdateAlertChannel(ctx, ch.ID, ch.Name, enc, ch.Enabled)
			}
			return
		}
	}
	tr := true
	cfg := &Config{
		YouTrackURL:   ytURL,
		YouTrackToken: ytToken,
		AutoResolve:   &tr,
	}
	// Try to auto-detect a default project key if there's only 1 project or pick the first non-DEMO project.
	if projs, err := s.fetchYTProjects(ctx, cfg); err == nil && len(projs) > 0 {
		chosen := projs[0].ShortName
		for _, p := range projs {
			if !strings.EqualFold(p.ShortName, "DEMO") {
				chosen = p.ShortName
				break
			}
		}
		cfg.YouTrackProj = chosen
	}
	enc, err := s.EncryptConfig(cfg)
	if err != nil {
		s.log.Error("youtrack: encrypt seeded config", "err", err)
		return
	}
	if _, err := s.store.CreateAlertChannel(ctx, TypeYouTrack, "YouTrack Cloud", enc, true, ""); err != nil {
		// created_by "" fails UUID cast if not null; let's handle nil created_by in CreateAlertChannel or query
		s.log.Warn("youtrack: seed channel", "err", err)
	} else {
		s.log.Info("youtrack: configured channel from .env", "url", ytURL, "default_project", cfg.YouTrackProj)
	}
}

// ActiveYouTrackConfig returns the decrypted config of the first enabled (or any configured) YouTrack channel.
func (s *Service) ActiveYouTrackConfig(ctx context.Context) (*Config, *store.AlertChannel, error) {
	channels, err := s.store.ListAlertChannels(ctx, false)
	if err != nil {
		return nil, nil, err
	}
	var fallback *store.AlertChannel
	for _, ch := range channels {
		if ch.Type == TypeYouTrack {
			if ch.Enabled {
				cfg, err := s.DecryptConfig(ch.ConfigEnc)
				if err != nil {
					return nil, nil, err
				}
				return cfg, ch, nil
			}
			if fallback == nil {
				fallback = ch
			}
		}
	}
	if fallback != nil {
		cfg, err := s.DecryptConfig(fallback.ConfigEnc)
		if err != nil {
			return nil, nil, err
		}
		return cfg, fallback, nil
	}
	return nil, nil, store.ErrNotFound
}

// ListYouTrackProjects fetches available non-archived YouTrack projects.
func (s *Service) ListYouTrackProjects(ctx context.Context) ([]YTProject, string, error) {
	cfg, _, err := s.ActiveYouTrackConfig(ctx)
	if err != nil {
		return nil, "", err
	}
	projs, err := s.fetchYTProjects(ctx, cfg)
	return projs, cfg.YouTrackURL, err
}

func (s *Service) fetchYTProjects(ctx context.Context, cfg *Config) ([]YTProject, error) {
	endpoint := strings.TrimRight(cfg.YouTrackURL, "/") + "/api/admin/projects?fields=id,name,shortName,archived"
	status, body, err := s.ytRequest(ctx, http.MethodGet, endpoint, cfg.YouTrackToken, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, ytAPIError(status, body)
	}
	var all []YTProject
	if err := json.Unmarshal(body, &all); err != nil {
		return nil, fmt.Errorf("invalid YouTrack response: %w", err)
	}
	out := make([]YTProject, 0, len(all))
	for _, p := range all {
		if !p.Archived {
			out = append(out, p)
		}
	}
	return out, nil
}

// ytDeliver handles an alert delivery to a YouTrack channel.
func (s *Service) ytDeliver(ctx context.Context, cfg *Config, m Message) error {
	if m.Event == "test" {
		projs, err := s.fetchYTProjects(ctx, cfg)
		if err != nil {
			return err
		}
		if len(projs) == 0 {
			return errors.New("connected to YouTrack, but no active projects were found")
		}
		return nil
	}
	if m.Event == EventNewDevice {
		// Security login notifications don't belong as project issues unless mapped.
		return nil
	}

	// If this is a recovery alert, comment on and optionally resolve the open YouTrack issue!
	if m.Event == EventCheckRecovered {
		if m.ResourceKey != "" {
			autoResolve := cfg.AutoResolve == nil || *cfg.AutoResolve
			return s.resolveYouTrackByDedupe(ctx, cfg, m.ResourceKey, m.youtrackCommentMarkdown(), autoResolve)
		}
		return nil
	}

	// Determine target YouTrack project shortName and Rexus Project ID.
	var rexusProj *store.Project
	if m.ProjectKey != "" {
		rexusProj, _ = s.store.ProjectByKey(ctx, m.ProjectKey)
	}
	ytProjKey := ""
	if rexusProj != nil && rexusProj.YouTrackProjectKey != nil && *rexusProj.YouTrackProjectKey != "" {
		ytProjKey = *rexusProj.YouTrackProjectKey
	} else if cfg.YouTrackProj != "" {
		ytProjKey = cfg.YouTrackProj
	}
	projs, err := s.fetchYTProjects(ctx, cfg)
	if err != nil {
		return err
	}
	var targetProj *YTProject
	if ytProjKey != "" {
		for i := range projs {
			if strings.EqualFold(projs[i].ShortName, ytProjKey) || projs[i].ID == ytProjKey {
				targetProj = &projs[i]
				break
			}
		}
	}
	// If no project was mapped yet, try matching Rexus project key/name, or fall back to first non-DEMO project.
	if targetProj == nil && rexusProj != nil {
		for i := range projs {
			if strings.EqualFold(projs[i].ShortName, rexusProj.Key) || strings.EqualFold(projs[i].Name, rexusProj.Name) {
				targetProj = &projs[i]
				break
			}
		}
	}
	if targetProj == nil && len(projs) > 0 {
		targetProj = &projs[0]
		for i := range projs {
			if !strings.EqualFold(projs[i].ShortName, "DEMO") {
				targetProj = &projs[i]
				break
			}
		}
	}
	if targetProj == nil {
		return errors.New("YouTrack has no active projects to create an issue in")
	}

	// Smart Deduplication (#5): check if there is already an open YouTrack issue for this resource!
	dedupeKey := m.ResourceKey
	if dedupeKey != "" {
		if existing, err := s.store.ActiveYouTrackLinkByDedupe(ctx, dedupeKey); err == nil && existing != nil {
			// Verify whether the issue is still open in YouTrack or add a comment.
			comment := m.youtrackCommentMarkdown()
			if err := s.addYTComment(ctx, cfg, existing.IssueID, comment); err == nil {
				_ = s.store.UpdateYouTrackLinkStatus(ctx, existing.ID, "", existing.State, existing.Priority, existing.Assignee, false)
				return nil
			}
		}
	}

	// Create a new YouTrack issue (`POST /api/issues`).
	summary := m.youtrackSummary()
	desc := m.youtrackDescriptionMarkdown()
	priority := severityToYTPriority(m.Severity)

	issue, err := s.createYTIssue(ctx, cfg, targetProj.ID, summary, desc, priority, "", m.DueDate)
	if err != nil {
		return err
	}

	if rexusProj != nil {
		var resID *string
		if m.ResourceID != "" {
			idCopy := m.ResourceID
			resID = &idCopy
		}
		var dKey *string
		if dedupeKey != "" {
			dkCopy := dedupeKey
			dKey = &dkCopy
		}
		resType := m.ResourceType
		if resType == "" {
			resType = "manual"
		}
		var prioPtr, assigneePtr *string
		if issue.Priority != "" {
			prioPtr = &issue.Priority
		}
		if issue.Assignee != "" {
			assigneePtr = &issue.Assignee
		}
		state := issue.State
		if state == "" {
			state = "Open"
		}
		_, _ = s.store.CreateYouTrackLink(ctx, &store.YouTrackLink{
			ProjectID:     rexusProj.ID,
			ResourceType:  resType,
			ResourceID:    resID,
			DedupeKey:     dKey,
			YTProjectKey:  targetProj.ShortName,
			IssueID:       issue.ID,
			IssueReadable: issue.IDReadable,
			IssueURL:      issue.URL,
			Summary:       issue.Summary,
			State:         state,
			Priority:      prioPtr,
			Assignee:      assigneePtr,
			Resolved:      issue.Resolved != nil,
		})
	}
	return nil
}

// ResolveResourceAsync comments on and resolves any open YouTrack issue for the given resource key in the background.
func (s *Service) ResolveResourceAsync(resourceKey, markdownComment string) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cfg, _, err := s.ActiveYouTrackConfig(ctx)
		if err != nil {
			return
		}
		autoResolve := cfg.AutoResolve == nil || *cfg.AutoResolve
		_ = s.resolveYouTrackByDedupe(ctx, cfg, resourceKey, markdownComment, autoResolve)
	}()
}

func (s *Service) resolveYouTrackByDedupe(ctx context.Context, cfg *Config, dedupeKey, comment string, autoResolve bool) error {
	link, err := s.store.ActiveYouTrackLinkByDedupe(ctx, dedupeKey)
	if err != nil || link == nil {
		return nil
	}
	if comment != "" {
		_ = s.addYTComment(ctx, cfg, link.IssueID, comment)
	}
	if autoResolve {
		// Use YouTrack command API to transition state to Done / Fixed / Resolved.
		for _, cmd := range []string{"State Done", "State Fixed", "State Resolved"} {
			if err := s.applyYTCommand(ctx, cfg, link.IssueID, cmd); err == nil {
				break
			}
		}
		_ = s.store.UpdateYouTrackLinkStatus(ctx, link.ID, link.Summary, "Done", link.Priority, link.Assignee, true)
	}
	return nil
}

// CreateManualIssue creates a YouTrack issue from a 1-click user action in Rexus and links it if tied to a resource.
func (s *Service) CreateManualIssue(ctx context.Context, in YTIssueCreateInput) (*store.YouTrackLink, *YTIssue, error) {
	cfg, _, err := s.ActiveYouTrackConfig(ctx)
	if err != nil {
		return nil, nil, errors.New("YouTrack is not configured yet — add a YouTrack channel under Alerts → Channels")
	}
	projs, err := s.fetchYTProjects(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	var targetProj *YTProject
	for i := range projs {
		if strings.EqualFold(projs[i].ShortName, in.YTProjectKey) || projs[i].ID == in.YTProjectKey {
			targetProj = &projs[i]
			break
		}
	}
	if targetProj == nil && len(projs) > 0 {
		targetProj = &projs[0]
	}
	if targetProj == nil {
		return nil, nil, errors.New("YouTrack project not found")
	}

	issue, err := s.createYTIssue(ctx, cfg, targetProj.ID, in.Summary, in.Description, in.Priority, in.Assignee, in.DueDate)
	if err != nil {
		return nil, nil, err
	}

	resType := in.ResourceType
	if resType == "" {
		resType = "manual"
	}
	var dedupe *string
	if in.ResourceID != nil && *in.ResourceID != "" && resType != "manual" {
		dk := resType + ":" + *in.ResourceID
		dedupe = &dk
	}
	var prioPtr, assigneePtr *string
	if issue.Priority != "" {
		prioPtr = &issue.Priority
	}
	if issue.Assignee != "" {
		assigneePtr = &issue.Assignee
	}
	state := issue.State
	if state == "" {
		state = "Open"
	}
	link, err := s.store.CreateYouTrackLink(ctx, &store.YouTrackLink{
		ProjectID:     in.ProjectID,
		ResourceType:  resType,
		ResourceID:    in.ResourceID,
		DedupeKey:     dedupe,
		YTProjectKey:  targetProj.ShortName,
		IssueID:       issue.ID,
		IssueReadable: issue.IDReadable,
		IssueURL:      issue.URL,
		Summary:       issue.Summary,
		State:         state,
		Priority:      prioPtr,
		Assignee:      assigneePtr,
		Resolved:      issue.Resolved != nil,
	})
	return link, issue, err
}

// FetchProjectIssues queries live issues from YouTrack for the given YouTrack project shortName
// and also refreshes Rexus's cached link statuses for that project.
func (s *Service) FetchProjectIssues(ctx context.Context, rexusProjectID, ytProjectKey string) ([]YTIssue, error) {
	cfg, _, err := s.ActiveYouTrackConfig(ctx)
	if err != nil {
		return nil, err
	}
	if ytProjectKey == "" {
		ytProjectKey = cfg.YouTrackProj
	}
	if ytProjectKey == "" {
		return []YTIssue{}, nil
	}
	q := fmt.Sprintf("project: %s", ytProjectKey)
	endpoint := strings.TrimRight(cfg.YouTrackURL, "/") +
		"/api/issues?fields=id,idReadable,summary,resolved,created,updated,customFields(name,value(name,login,fullName))&$top=50&query=" +
		url.QueryEscape(q)
	status, body, err := s.ytRequest(ctx, http.MethodGet, endpoint, cfg.YouTrackToken, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, ytAPIError(status, body)
	}
	var raw []rawYTIssue
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	baseURL := strings.TrimRight(cfg.YouTrackURL, "/")
	issues := make([]YTIssue, 0, len(raw))
	byID := map[string]YTIssue{}
	for _, r := range raw {
		iss := r.toYTIssue(baseURL)
		issues = append(issues, iss)
		byID[iss.ID] = iss
		byID[iss.IDReadable] = iss
	}

	// Sync cached link statuses in Rexus DB so badges stay up to date!
	if rexusProjectID != "" {
		if links, err := s.store.ListYouTrackLinks(ctx, store.Access{}, rexusProjectID, false, 100); err == nil {
			for _, l := range links {
				if live, ok := byID[l.IssueID]; ok {
					var prio, ass *string
					if live.Priority != "" {
						p := live.Priority
						prio = &p
					}
					if live.Assignee != "" {
						a := live.Assignee
						ass = &a
					}
					_ = s.store.UpdateYouTrackLinkStatus(ctx, l.ID, live.Summary, live.State, prio, ass, live.Resolved != nil)
				}
			}
		}
	}
	return issues, nil
}

// RefreshOpenLinks syncs all unresolved youtrack_links in the database against YouTrack.
func (s *Service) RefreshOpenLinks(ctx context.Context) {
	cfg, _, err := s.ActiveYouTrackConfig(ctx)
	if err != nil {
		return
	}
	links, err := s.store.ListYouTrackLinks(ctx, store.Access{}, "", true, 50)
	if err != nil || len(links) == 0 {
		return
	}
	baseURL := strings.TrimRight(cfg.YouTrackURL, "/")
	for _, l := range links {
		endpoint := baseURL + "/api/issues/" + url.PathEscape(l.IssueID) +
			"?fields=id,idReadable,summary,resolved,created,updated,customFields(name,value(name,login,fullName))"
		status, body, err := s.ytRequest(ctx, http.MethodGet, endpoint, cfg.YouTrackToken, nil)
		if err != nil || status != http.StatusOK {
			continue
		}
		var r rawYTIssue
		if json.Unmarshal(body, &r) == nil && r.ID != "" {
			live := r.toYTIssue(baseURL)
			var prio, ass *string
			if live.Priority != "" {
				p := live.Priority
				prio = &p
			}
			if live.Assignee != "" {
				a := live.Assignee
				ass = &a
			}
			_ = s.store.UpdateYouTrackLinkStatus(ctx, l.ID, live.Summary, live.State, prio, ass, live.Resolved != nil)
		}
	}
}

type rawYTIssue struct {
	ID           string `json:"id"`
	IDReadable   string `json:"idReadable"`
	Summary      string `json:"summary"`
	Resolved     *int64 `json:"resolved"`
	Created      int64  `json:"created"`
	Updated      int64  `json:"updated"`
	CustomFields []struct {
		Name  string          `json:"name"`
		Value json.RawMessage `json:"value"`
	} `json:"customFields"`
}

func (r rawYTIssue) toYTIssue(baseURL string) YTIssue {
	iss := YTIssue{
		ID:         r.ID,
		IDReadable: r.IDReadable,
		Summary:    r.Summary,
		Resolved:   r.Resolved,
		Created:    r.Created,
		Updated:    r.Updated,
		URL:        baseURL + "/issue/" + r.IDReadable,
		State:      "Open",
	}
	for _, cf := range r.CustomFields {
		if len(cf.Value) == 0 || string(cf.Value) == "null" {
			continue
		}
		var obj struct {
			Name     string `json:"name"`
			Login    string `json:"login"`
			FullName string `json:"fullName"`
		}
		if json.Unmarshal(cf.Value, &obj) == nil {
			val := obj.Name
			if val == "" {
				val = obj.FullName
			}
			if val == "" {
				val = obj.Login
			}
			switch strings.ToLower(cf.Name) {
			case "state":
				if val != "" {
					iss.State = val
				}
			case "priority":
				iss.Priority = val
			case "assignee":
				if obj.Login != "" {
					iss.Assignee = obj.Login
				} else {
					iss.Assignee = val
				}
			case "type":
				iss.Type = val
			}
		}
	}
	if iss.Resolved != nil && iss.State == "Open" {
		iss.State = "Done"
	}
	return iss
}

func (s *Service) createYTIssue(ctx context.Context, cfg *Config, ytProjectID, summary, description, priority, assignee, dueDate string) (*YTIssue, error) {
	baseURL := strings.TrimRight(cfg.YouTrackURL, "/")
	payload := map[string]any{
		"project":     map[string]any{"id": ytProjectID},
		"summary":     clip(oneLine(summary), 240),
		"description": description,
	}
	endpoint := baseURL + "/api/issues?fields=id,idReadable,summary,resolved,created,updated,customFields(name,value(name,login,fullName))"
	status, body, err := s.ytRequest(ctx, http.MethodPost, endpoint, cfg.YouTrackToken, payload)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, ytAPIError(status, body)
	}
	var raw rawYTIssue
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("invalid YouTrack issue response: %w", err)
	}
	iss := raw.toYTIssue(baseURL)

	// Apply optional fields via commands so we never fail issue creation if a project uses a different custom field schema.
	var cmds []string
	if priority != "" {
		cmds = append(cmds, "Priority "+priority)
	}
	if assignee != "" {
		cmds = append(cmds, "Assignee "+assignee)
	}
	if dueDate != "" {
		cmds = append(cmds, "Due Date "+dueDate)
	}
	for _, cmd := range cmds {
		_ = s.applyYTCommand(ctx, cfg, iss.ID, cmd)
	}
	if priority != "" {
		iss.Priority = priority
	}
	if assignee != "" {
		iss.Assignee = assignee
	}
	return &iss, nil
}

func (s *Service) addYTComment(ctx context.Context, cfg *Config, issueID, text string) error {
	endpoint := strings.TrimRight(cfg.YouTrackURL, "/") + "/api/issues/" + url.PathEscape(issueID) + "/comments"
	status, body, err := s.ytRequest(ctx, http.MethodPost, endpoint, cfg.YouTrackToken, map[string]any{
		"text": text,
	})
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return ytAPIError(status, body)
	}
	return nil
}

func (s *Service) applyYTCommand(ctx context.Context, cfg *Config, issueID, query string) error {
	endpoint := strings.TrimRight(cfg.YouTrackURL, "/") + "/api/commands"
	status, body, err := s.ytRequest(ctx, http.MethodPost, endpoint, cfg.YouTrackToken, map[string]any{
		"query":  query,
		"issues": []map[string]any{{"id": issueID}},
	})
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return ytAPIError(status, body)
	}
	return nil
}

func (s *Service) ytRequest(ctx context.Context, method, endpoint, token string, payload any) (int, []byte, error) {
	var bodyReader io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bodyReader)
	if err != nil {
		return 0, nil, &sendError{msg: "invalid YouTrack URL"}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "Rexus-YouTrack/1.0")
	resp, err := s.sender.http.Do(req)
	if err != nil {
		return 0, nil, &sendError{msg: cleanErr(err), retry: true}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	return resp.StatusCode, b, nil
}

func ytAPIError(status int, body []byte) error {
	var r struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &r)
	msg := fmt.Sprintf("YouTrack returned HTTP %d", status)
	if r.ErrorDescription != "" {
		msg = "YouTrack: " + clip(r.ErrorDescription, 200)
	} else if r.Error != "" {
		msg = "YouTrack: " + clip(r.Error, 200)
	}
	return &sendError{msg: msg, retry: retryable(status)}
}

func severityToYTPriority(sev string) string {
	switch sev {
	case "critical":
		return "Critical"
	case "warning":
		return "Major"
	default:
		return "Normal"
	}
}

func (m Message) youtrackSummary() string {
	return "[" + strings.ToUpper(m.Severity) + "] " + m.headline()
}

func (m Message) youtrackDescriptionMarkdown() string {
	var b strings.Builder
	b.WriteString("### " + emojiFor(m.Severity) + " " + m.headline() + "\n\n")
	if m.ProjectKey != "" {
		b.WriteString("- **Project:** `" + m.ProjectKey + "`\n")
	}
	if m.ServiceKey != "" {
		b.WriteString("- **Service:** `" + m.ServiceKey + "`\n")
	}
	if m.Environment != "" {
		b.WriteString("- **Environment:** `" + m.Environment + "`\n")
	}
	b.WriteString("- **Event:** `" + m.Event + "`\n")
	if m.DueDate != "" {
		b.WriteString("- **Expiry / Due Date:** **" + m.DueDate + "**\n")
	}
	b.WriteString("\n#### Details\n")
	for _, l := range m.lines() {
		b.WriteString("- " + l + "\n")
	}
	if m.Snippet != "" {
		b.WriteString("\n#### Response Snippet\n```\n" + clip(m.Snippet, 1200) + "\n```\n")
	}
	if m.Link != "" {
		b.WriteString("\n---\n[Open in Rexus](" + m.Link + ")\n")
	}
	return b.String()
}

func (m Message) youtrackCommentMarkdown() string {
	var b strings.Builder
	b.WriteString("**" + emojiFor(m.Severity) + " " + m.headline() + "**\n\n")
	for _, l := range m.lines() {
		b.WriteString("- " + l + "\n")
	}
	if m.Link != "" {
		b.WriteString("\n[View in Rexus](" + m.Link + ")")
	}
	return b.String()
}
