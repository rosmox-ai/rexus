package store

import (
	"context"
	"time"
)

// YouTrackLink tracks a YouTrack issue created from Rexus (automatically or manually)
// so Rexus can show live ticket badges, avoid duplicate tickets, and auto-resolve on recovery.
type YouTrackLink struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	ProjectKey    string    `json:"project_key"`
	ResourceType  string    `json:"resource_type"` // check | job | secret | ssl | manual
	ResourceID    *string   `json:"resource_id"`
	DedupeKey     *string   `json:"dedupe_key"`
	YTProjectKey  string    `json:"yt_project_key"`
	IssueID       string    `json:"issue_id"`
	IssueReadable string    `json:"issue_readable"` // e.g. VID-42
	IssueURL      string    `json:"issue_url"`
	Summary       string    `json:"summary"`
	State         string    `json:"state"`
	Priority      *string   `json:"priority"`
	Assignee      *string   `json:"assignee"`
	Resolved      bool      `json:"resolved"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

const ytLinkSelect = `SELECT l.id, l.project_id, p.key, l.resource_type, l.resource_id, l.dedupe_key,
	l.yt_project_key, l.issue_id, l.issue_readable, l.issue_url, l.summary, l.state, l.priority, l.assignee,
	l.resolved, l.created_at, l.updated_at
	FROM youtrack_links l
	JOIN projects p ON p.id = l.project_id`

func scanYTLink(row interface{ Scan(...any) error }) (*YouTrackLink, error) {
	l := &YouTrackLink{}
	err := row.Scan(&l.ID, &l.ProjectID, &l.ProjectKey, &l.ResourceType, &l.ResourceID, &l.DedupeKey,
		&l.YTProjectKey, &l.IssueID, &l.IssueReadable, &l.IssueURL, &l.Summary, &l.State, &l.Priority, &l.Assignee,
		&l.Resolved, &l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return l, nil
}

// ActiveYouTrackLinkByDedupe finds an unresolved YouTrack issue link for the given dedupe key.
func (s *Store) ActiveYouTrackLinkByDedupe(ctx context.Context, dedupeKey string) (*YouTrackLink, error) {
	return scanYTLink(s.Pool.QueryRow(ctx, ytLinkSelect+` WHERE l.dedupe_key = $1 AND NOT l.resolved ORDER BY l.created_at DESC LIMIT 1`, dedupeKey))
}

// ActiveYouTrackLinksForResource finds unresolved YouTrack links for a specific resource.
func (s *Store) ActiveYouTrackLinksForResource(ctx context.Context, resourceType, resourceID string) ([]*YouTrackLink, error) {
	rows, err := s.Pool.Query(ctx, ytLinkSelect+` WHERE l.resource_type = $1 AND l.resource_id = $2 AND NOT l.resolved ORDER BY l.created_at DESC`, resourceType, resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*YouTrackLink
	for rows.Next() {
		l, err := scanYTLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// CreateYouTrackLink saves a newly created YouTrack ticket link.
func (s *Store) CreateYouTrackLink(ctx context.Context, l *YouTrackLink) (*YouTrackLink, error) {
	var id string
	err := s.Pool.QueryRow(ctx, `INSERT INTO youtrack_links (
			project_id, resource_type, resource_id, dedupe_key, yt_project_key,
			issue_id, issue_readable, issue_url, summary, state, priority, assignee, resolved
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING id`,
		l.ProjectID, l.ResourceType, l.ResourceID, l.DedupeKey, l.YTProjectKey,
		l.IssueID, l.IssueReadable, l.IssueURL, l.Summary, l.State, l.Priority, l.Assignee, l.Resolved,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	return scanYTLink(s.Pool.QueryRow(ctx, ytLinkSelect+` WHERE l.id = $1`, id))
}

// UpdateYouTrackLinkStatus updates the cached state, priority, assignee, summary, and resolved flag of a link.
func (s *Store) UpdateYouTrackLinkStatus(ctx context.Context, id, summary, state string, priority, assignee *string, resolved bool) error {
	_, err := s.Pool.Exec(ctx, `UPDATE youtrack_links SET
		summary = COALESCE(NULLIF($2, ''), summary),
		state = COALESCE(NULLIF($3, ''), state),
		priority = $4,
		assignee = $5,
		resolved = $6,
		updated_at = now()
		WHERE id = $1`, id, summary, state, priority, assignee, resolved)
	return err
}

// ListYouTrackLinks returns tracked YouTrack issues visible to the caller.
func (s *Store) ListYouTrackLinks(ctx context.Context, a Access, projectID string, onlyUnresolved bool, limit int) ([]*YouTrackLink, error) {
	var b filterBuilder
	if projectID != "" {
		b.add("l.project_id = ?", projectID)
	}
	if onlyUnresolved {
		b.add("l.resolved = ?", false)
	}
	b.access("l.project_id", a)
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.Pool.Query(ctx, ytLinkSelect+b.sql()+` ORDER BY l.resolved ASC, l.updated_at DESC LIMIT 100`, b.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*YouTrackLink{}
	for rows.Next() {
		l, err := scanYTLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
