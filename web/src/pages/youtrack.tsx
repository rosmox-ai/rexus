import { useCallback, useEffect, useState, type FormEvent, type MouseEvent } from 'react'
import { Link } from 'react-router-dom'
import { api, type Project, type YouTrackIssue, type YouTrackLink, type YouTrackProject } from '../api'
import { NavIcon } from '../icons'
import { Badge, Button, Card, Empty, ErrorBox, Field, Input, Modal, Select, Spinner, Textarea, cx, timeAgo } from '../ui'

export type { YouTrackLink, YouTrackIssue, YouTrackProject }

export interface YouTrackStatus {
  configured: boolean
  base_url: string
  default_project: string
  projects: YouTrackProject[]
  error?: string
}

export function useYouTrackStatus() {
  const [status, setStatus] = useState<YouTrackStatus | null>(null)
  const reload = useCallback(() => {
    api<YouTrackStatus>('/api/youtrack/status')
      .then(setStatus)
      .catch(() => setStatus({ configured: false, base_url: '', default_project: '', projects: [] }))
  }, [])
  useEffect(() => {
    reload()
  }, [reload])
  return { status, reload }
}

export function useYouTrackLinks(projectKey?: string) {
  const [links, setLinks] = useState<YouTrackLink[]>([])
  const reload = useCallback(() => {
    const q = projectKey ? `?project=${encodeURIComponent(projectKey)}&unresolved=1` : '?unresolved=1'
    api<{ links: YouTrackLink[] }>(`/api/youtrack/links${q}`)
      .then((r) => setLinks(r.links ?? []))
      .catch(() => {})
  }, [projectKey])

  useEffect(() => {
    reload()
  }, [reload])

  const forResource = useCallback(
    (type: YouTrackLink['resource_type'], id: string): YouTrackLink | undefined =>
      links.find((l) => !l.resolved && l.resource_type === type && l.resource_id === id),
    [links],
  )

  return { links, forResource, reload }
}

function stateTone(state: string, resolved?: boolean): 'green' | 'amber' | 'sky' | 'zinc' {
  if (resolved) return 'green'
  const s = state.toLowerCase()
  if (s === 'done' || s === 'fixed' || s === 'resolved' || s === 'verified') return 'green'
  if (s.includes('progress') || s.includes('review')) return 'amber'
  if (s.includes('hold') || s.includes('cancel')) return 'zinc'
  return 'sky'
}

function priorityTone(priority: string | null | undefined): 'red' | 'amber' | 'zinc' {
  const p = (priority ?? '').toLowerCase()
  if (p === 'show-stopper' || p === 'critical') return 'red'
  if (p === 'major') return 'amber'
  return 'zinc'
}

/** Live ticket badge shown on Dashboard, Health Checks, Jobs, and Secrets (#8). */
export function YouTrackBadge({ link }: { link?: YouTrackLink }) {
  if (!link) return null
  const onClick = (e: MouseEvent) => {
    e.stopPropagation()
  }
  return (
    <a
      href={link.issue_url}
      target="_blank"
      rel="noopener noreferrer"
      onClick={onClick}
      title={`${link.issue_readable}: ${link.summary}${link.priority ? ` (${link.priority})` : ''}`}
      className="inline-flex items-center gap-1.5 rounded border border-sky-800/80 bg-sky-950/50 px-2 py-0.5 text-xs font-medium text-sky-200 transition hover:border-sky-600 hover:bg-sky-900/50"
    >
      <span className="mono font-semibold text-sky-300">{link.issue_readable}</span>
      <span className="text-sky-400/70">·</span>
      <span>{link.state || 'Open'}</span>
      {link.assignee && (
        <>
          <span className="text-sky-400/70">·</span>
          <span className="text-zinc-300">@{link.assignee}</span>
        </>
      )}
      <NavIcon name="external" size={11} />
    </a>
  )
}

export interface IssuePrefill {
  projectKey: string
  ytProjectKey?: string | null
  summary: string
  description: string
  priority?: string
  dueDate?: string
  resourceType?: YouTrackLink['resource_type']
  resourceId?: string
}

/** 1-Click "Create YouTrack Issue" modal (#7). */
export function CreateYouTrackIssueModal({
  prefill,
  onClose,
  onCreated,
}: {
  prefill: IssuePrefill
  onClose: () => void
  onCreated: (link: YouTrackLink, issue: YouTrackIssue) => void
}) {
  const { status } = useYouTrackStatus()
  const [ytProject, setYtProject] = useState(prefill.ytProjectKey || '')
  const [summary, setSummary] = useState(prefill.summary)
  const [description, setDescription] = useState(prefill.description)
  const [priority, setPriority] = useState(prefill.priority || 'Normal')
  const [assignee, setAssignee] = useState('')
  const [dueDate, setDueDate] = useState(prefill.dueDate || '')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!ytProject && status) {
      const def = prefill.ytProjectKey || status.default_project || status.projects[0]?.shortName || ''
      if (def) setYtProject(def)
    }
  }, [status, ytProject, prefill.ytProjectKey])

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const r = await api<{ link: YouTrackLink; issue: YouTrackIssue }>(
        `/api/projects/${encodeURIComponent(prefill.projectKey)}/youtrack/issues`,
        {
          method: 'POST',
          body: {
            yt_project_key: ytProject,
            summary,
            description,
            priority,
            assignee,
            due_date: dueDate,
            resource_type: prefill.resourceType ?? 'manual',
            resource_id: prefill.resourceId ?? null,
          },
        },
      )
      onCreated(r.link, r.issue)
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title="Create YouTrack issue" onClose={onClose} wide>
      {status && !status.configured ? (
        <div className="space-y-3 text-sm text-zinc-300">
          <p>YouTrack is not configured yet.</p>
          <p className="text-xs text-zinc-400">
            Add a YouTrack channel under <Link to="/alerts?view=channels" className="text-emerald-400 underline">Alerts → Channels</Link> or set{' '}
            <code>REXUS_YOUTRACK_URL</code> and <code>REXUS_YOUTRACK_TOKEN</code> in <code>.env</code>.
          </p>
          <div className="flex justify-end">
            <Button variant="ghost" onClick={onClose}>Close</Button>
          </div>
        </div>
      ) : (
        <form onSubmit={submit} className="space-y-4">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field label="YouTrack project">
              {status && status.projects.length > 0 ? (
                <Select value={ytProject} onChange={(e) => setYtProject(e.target.value)} required>
                  {status.projects.map((p) => (
                    <option key={p.id} value={p.shortName}>
                      {p.name} ({p.shortName})
                    </option>
                  ))}
                </Select>
              ) : (
                <Input value={ytProject} onChange={(e) => setYtProject(e.target.value)} className="mono" placeholder="e.g. VID" required />
              )}
            </Field>
            <Field label="Priority">
              <Select value={priority} onChange={(e) => setPriority(e.target.value)}>
                <option value="Show-stopper">Show-stopper</option>
                <option value="Critical">Critical</option>
                <option value="Major">Major</option>
                <option value="Normal">Normal</option>
                <option value="Minor">Minor</option>
              </Select>
            </Field>
          </div>

          <Field label="Summary">
            <Input value={summary} onChange={(e) => setSummary(e.target.value)} required autoFocus placeholder="Issue summary" />
          </Field>

          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field label="Assignee (optional)" hint="YouTrack username, e.g. anil1991das">
              <Input value={assignee} onChange={(e) => setAssignee(e.target.value)} className="mono" placeholder="me or login" />
            </Field>
            <Field label="Due date (optional)">
              <Input type="date" value={dueDate} onChange={(e) => setDueDate(e.target.value)} />
            </Field>
          </div>

          <Field label="Description (Markdown)">
            <Textarea value={description} onChange={(e) => setDescription(e.target.value)} rows={6} className="mono text-xs" />
          </Field>

          <ErrorBox error={error} />
          <div className="flex justify-end gap-2 border-t border-zinc-800 pt-3">
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Button type="submit" disabled={busy}>{busy ? 'Creating in YouTrack…' : 'Create issue'}</Button>
          </div>
        </form>
      )}
    </Modal>
  )
}

interface ProjectYTResp {
  configured: boolean
  base_url: string
  yt_project_key: string
  issues: YouTrackIssue[]
  links: YouTrackLink[]
  error?: string
}

/** Project page YouTrack panel (#2 Project mapping + #9 Open YouTrack issues panel). */
export function ProjectYouTrackSection({
  project,
  writable,
  admin,
  onProjectUpdated,
}: {
  project: Project
  writable: boolean
  admin: boolean
  onProjectUpdated: (p: Project) => void
}) {
  const { status } = useYouTrackStatus()
  const [data, setData] = useState<ProjectYTResp | null>(null)
  const [loading, setLoading] = useState(true)
  const [showResolved, setShowResolved] = useState(false)
  const [creating, setCreating] = useState(false)
  const [savingMap, setSavingMap] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const r = await api<ProjectYTResp>(`/api/projects/${encodeURIComponent(project.key)}/youtrack/issues`)
      setData(r)
    } catch {
      // ignore
    } finally {
      setLoading(false)
    }
  }, [project.key])

  useEffect(() => {
    void load()
  }, [load, project.youtrack_project_key])

  if (status && !status.configured) return null

  const changeMappedProject = async (ytKey: string) => {
    setSavingMap(true)
    try {
      const r = await api<{ project: Project }>(`/api/projects/${encodeURIComponent(project.key)}`, {
        method: 'PATCH',
        body: { youtrack_project_key: ytKey },
      })
      onProjectUpdated(r.project)
    } finally {
      setSavingMap(false)
    }
  }

  const activeYTKey = project.youtrack_project_key || data?.yt_project_key || status?.default_project || ''
  const allIssues = data?.issues ?? []
  const openIssues = allIssues.filter((i) => i.resolved == null)
  const shownIssues = showResolved ? allIssues : openIssues
  const boardUrl = data?.base_url && activeYTKey ? `${data.base_url.replace(/\/$/, '')}/issues/${encodeURIComponent(activeYTKey)}` : data?.base_url

  return (
    <section id="youtrack" className="mt-8 scroll-mt-6">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2.5">
          <h2 className="font-medium text-zinc-200">YouTrack issues</h2>
          {activeYTKey && <Badge tone="sky">{activeYTKey}</Badge>}
          {data && <span className="text-xs text-zinc-500">{openIssues.length} open</span>}
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {admin && status && status.projects.length > 0 && (
            <Select
              aria-label="Linked YouTrack project"
              value={project.youtrack_project_key ?? ''}
              disabled={savingMap}
              onChange={(e) => void changeMappedProject(e.target.value)}
              className="w-auto! py-1 text-xs"
            >
              <option value="">
                Default ({status.default_project || status.projects[0]?.shortName || 'auto'})
              </option>
              {status.projects.map((p) => (
                <option key={p.id} value={p.shortName}>
                  Map to {p.name} ({p.shortName})
                </option>
              ))}
            </Select>
          )}
          { allIssues.length > openIssues.length && (
            <button
              type="button"
              onClick={() => setShowResolved((v) => !v)}
              className="px-2 py-1 text-xs text-zinc-400 hover:text-zinc-200"
            >
              {showResolved ? 'Hide resolved' : `Show resolved (${allIssues.length - openIssues.length})`}
            </button>
          )}
          {boardUrl && (
            <a href={boardUrl} target="_blank" rel="noopener noreferrer">
              <Button variant="ghost" className="py-1 text-xs">
                Open YouTrack <NavIcon name="external" size={13} />
              </Button>
            </a>
          )}
          {writable && (
            <Button variant="secondary" onClick={() => setCreating(true)}>
              New issue
            </Button>
          )}
        </div>
      </div>

      {data?.error && <ErrorBox error={data.error} />}

      {loading && !data ? (
        <Spinner />
      ) : shownIssues.length === 0 ? (
        <Empty title={openIssues.length === 0 ? `No open issues in ${activeYTKey || 'YouTrack'}` : 'No issues'}>
          Failed checks, failing jobs, and expiring secrets automatically open tickets here, or click <b>New issue</b> to create one.
        </Empty>
      ) : (
        <Card className="overflow-x-auto">
          <table className="w-full min-w-[600px] text-left text-sm">
            <thead className="border-b border-zinc-800 text-xs text-zinc-500">
              <tr>
                <th className="px-4 py-2 font-medium">Issue</th>
                <th className="px-4 py-2 font-medium">State</th>
                <th className="px-4 py-2 font-medium">Priority</th>
                <th className="px-4 py-2 font-medium">Assignee</th>
                <th className="px-4 py-2 text-right font-medium">Updated</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-zinc-800/70">
              {shownIssues.slice(0, 15).map((iss) => (
                <tr key={iss.id} className="hover:bg-zinc-800/30">
                  <td className="max-w-md px-4 py-2.5">
                    <a
                      href={iss.url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="group flex items-center gap-2"
                    >
                      <span className={cx('mono shrink-0 text-xs font-semibold', iss.resolved ? 'text-zinc-500 line-through' : 'text-sky-400 group-hover:underline')}>
                        {iss.idReadable}
                      </span>
                      <span className={cx('truncate', iss.resolved ? 'text-zinc-500' : 'text-zinc-100 group-hover:underline')}>
                        {iss.summary}
                      </span>
                      <span className="shrink-0 text-zinc-500 opacity-0 transition group-hover:opacity-100">
                        <NavIcon name="external" size={12} />
                      </span>
                    </a>
                  </td>
                  <td className="px-4 py-2.5">
                    <Badge tone={stateTone(iss.state, iss.resolved != null)}>{iss.state}</Badge>
                  </td>
                  <td className="px-4 py-2.5">
                    {iss.priority ? <Badge tone={priorityTone(iss.priority)}>{iss.priority}</Badge> : <span className="text-xs text-zinc-600">—</span>}
                  </td>
                  <td className="px-4 py-2.5 text-xs text-zinc-300">
                    {iss.assignee ? `@${iss.assignee}` : <span className="text-zinc-600">Unassigned</span>}
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5 text-right text-xs text-zinc-500">
                    {iss.updated ? timeAgo(new Date(iss.updated).toISOString()) : '—'}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
      )}

      {creating && (
        <CreateYouTrackIssueModal
          prefill={{
            projectKey: project.key,
            ytProjectKey: activeYTKey,
            summary: `[${project.name}] `,
            description: `Created from Rexus project **${project.name}** (\`${project.key}\`).\n`,
            priority: 'Normal',
            resourceType: 'manual',
          }}
          onClose={() => setCreating(false)}
          onCreated={() => {
            setCreating(false)
            void load()
          }}
        />
      )}
    </section>
  )
}
