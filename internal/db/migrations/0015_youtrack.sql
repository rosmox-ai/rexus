-- JetBrains YouTrack integration:
-- 1. Allow 'youtrack' as an alert channel type.
-- 2. Map Rexus projects to YouTrack project shortNames (e.g. ACME, VID).
-- 3. Track linked YouTrack issues for checks, jobs, secrets, and manual tickets.

ALTER TABLE alert_channels DROP CONSTRAINT IF EXISTS alert_channels_type_check;
ALTER TABLE alert_channels ADD CONSTRAINT alert_channels_type_check
    CHECK (type IN ('telegram', 'discord', 'slack', 'email', 'youtrack'));

ALTER TABLE projects ADD COLUMN youtrack_project_key text CHECK (length(youtrack_project_key) <= 40);

CREATE TABLE youtrack_links (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id     uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    resource_type  text NOT NULL CHECK (resource_type IN ('check', 'job', 'secret', 'ssl', 'manual')),
    resource_id    uuid,                          -- check_id, job_id, or secret_id
    dedupe_key     text,                          -- e.g. check:UUID, job:UUID, secret:UUID, ssl:UUID
    yt_project_key text NOT NULL,                 -- e.g. VID, DEMO
    issue_id       text NOT NULL,                 -- YouTrack internal ID (e.g. 3-14)
    issue_readable text NOT NULL,                 -- YouTrack readable ID (e.g. VID-42)
    issue_url      text NOT NULL,                 -- https://.../issue/VID-42
    summary        text NOT NULL,
    state          text NOT NULL DEFAULT 'Open',  -- Open, In Progress, Done, etc.
    priority       text,
    assignee       text,
    resolved       boolean NOT NULL DEFAULT false,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX youtrack_links_project_idx ON youtrack_links (project_id, resolved, updated_at DESC);
CREATE INDEX youtrack_links_resource_idx ON youtrack_links (resource_type, resource_id) WHERE resource_id IS NOT NULL;
CREATE INDEX youtrack_links_dedupe_idx ON youtrack_links (dedupe_key) WHERE NOT resolved AND dedupe_key IS NOT NULL;
