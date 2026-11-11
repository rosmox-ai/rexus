import { useCallback, useEffect, useMemo, useState, type FormEvent, type MouseEvent } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { api, canWrite, isAdmin, type Project, type YouTrackIssue, type YouTrackLink, type YouTrackProject } from '../api'
import { useAuth } from '../auth'
import { NavIcon } from '../icons'
import { Badge, Button, Card, Empty, ErrorBox, Field, Input, Modal, PageHeader, Select, Spinner, Textarea, cx, fmtTime, timeAgo } from '../ui'

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

const SOURCE_LABEL: Record<YouTrackLink['resource_type'], string> = {
  check: 'Health check',
  job: 'Job',
  secret: 'Secret',
  ssl: 'SSL cert',
  manual: 'Manual',
}

function sourceLink(l: YouTrackLink): string {
  if ((l.resource_type === 'check' || l.resource_type === 'ssl') && l.resource_id) return `/checks/${l.resource_id}`
  if (l.resource_type === 'job' && l.resource_id) return `/jobs/${l.resource_id}`
  return `/projects/${l.project_key}`
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
  projects?: Project[]
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
  const [rexusProjectKey, setRexusProjectKey] = useState(prefill.projectKey || prefill.projects?.[0]?.key || '')
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
      const mapped = prefill.projects?.find((p) => p.key === rexusProjectKey)?.youtrack_project_key
      const def = prefill.ytProjectKey || mapped || status.default_project || status.projects[0]?.shortName || ''
      if (def) setYtProject(def)
    }
  }, [status, ytProject, prefill.ytProjectKey, prefill.projects, rexusProjectKey])

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (!rexusProjectKey) {
      setError('Please choose a Rexus project.')
      return
    }
    setBusy(true)
    setError(null)
    try {
      const r = await api<{ link: YouTrackLink; issue: YouTrackIssue }>(
        `/api/projects/${encodeURIComponent(rexusProjectKey)}/youtrack/issues`,
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
          {prefill.projects && prefill.projects.length > 0 && !prefill.projectKey && (
            <Field label="Rexus project">
              <Select
                value={rexusProjectKey}
                onChange={(e) => {
                  const nextKey = e.target.value
                  setRexusProjectKey(nextKey)
                  const p = prefill.projects?.find((x) => x.key === nextKey)
                  if (p?.youtrack_project_key) setYtProject(p.youtrack_project_key)
                }}
                required
              >
                {prefill.projects.map((p) => (
                  <option key={p.id} value={p.key}>
                    {p.name} ({p.key})
                  </option>
                ))}
              </Select>
            </Field>
          )}

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

/** Centralized Tickets page (/tickets) for all YouTrack issues raised from Rexus across all projects. */
export function TicketsPage() {
  const { user } = useAuth()
  const writable = !!user && canWrite(user.role)
  const admin = !!user && isAdmin(user.role)
  const { status } = useYouTrackStatus()
  const [links, setLinks] = useState<YouTrackLink[] | null>(null)
  const [projects, setProjects] = useState<Project[]>([])
  const [error, setError] = useState<string | null>(null)
  const [syncing, setSyncing] = useState(false)
  const [creating, setCreating] = useState(false)
  const [params, setParams] = useSearchParams()

  const q = params.get('q') ?? ''
  const stateFilter = params.get('state') ?? 'open' // 'open' | 'resolved' | 'all'
  const projectFilter = params.get('project') ?? ''
  const sourceFilter = params.get('source') ?? ''

  const setParam = (name: string, value: string) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        if (value) next.set(name, value)
        else next.delete(name)
        return next
      },
      { replace: true },
    )

  const load = useCallback(async () => {
    try {
      const [l, p] = await Promise.all([
        api<{ links: YouTrackLink[] }>('/api/youtrack/links?unresolved=0'),
        api<{ projects: Project[] }>('/api/projects'),
      ])
      setLinks(l.links ?? [])
      setProjects(p.projects ?? [])
      setError(null)
    } catch (e) {
      setError((e as Error).message)
    }
  }, [])

  useEffect(() => {
    void load()
    const t = setInterval(load, 30_000)
    return () => clearInterval(t)
  }, [load])

  const syncNow = async () => {
    setSyncing(true)
    setError(null)
    try {
      const r = await api<{ links: YouTrackLink[] }>('/api/youtrack/sync', { method: 'POST' })
      setLinks(r.links ?? [])
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSyncing(false)
    }
  }

  const allLinks = useMemo(() => links ?? [], [links])
  const openLinks = useMemo(() => allLinks.filter((l) => !l.resolved), [allLinks])
  const resolvedLinks = useMemo(() => allLinks.filter((l) => l.resolved), [allLinks])
  const highPriorityOpen = useMemo(
    () =>
      openLinks.filter((l) => {
        const p = (l.priority ?? '').toLowerCase()
        return p === 'show-stopper' || p === 'critical' || p === 'major'
      }),
    [openLinks],
  )
  const autoRaisedOpen = useMemo(() => openLinks.filter((l) => l.resource_type !== 'manual'), [openLinks])

  const shown = useMemo(() => {
    const needle = q.trim().toLowerCase()
    return allLinks.filter((l) => {
      if (stateFilter === 'open' && l.resolved) return false
      if (stateFilter === 'resolved' && !l.resolved) return false
      if (projectFilter && l.project_key !== projectFilter) return false
      if (sourceFilter && l.resource_type !== sourceFilter) return false
      if (needle) {
        const hay = `${l.issue_readable} ${l.summary} ${l.project_name} ${l.project_key} ${l.resource_name ?? ''} ${l.assignee ?? ''} ${l.state}`.toLowerCase()
        if (!hay.includes(needle)) return false
      }
      return true
    })
  }, [allLinks, q, stateFilter, projectFilter, sourceFilter])

  if (!links && !error) return <Spinner />

  const filtered = !!(q || stateFilter !== 'open' || projectFilter || sourceFilter)
  const clearFilters = () => setParams(new URLSearchParams(), { replace: true })

  return (
    <>
      <PageHeader
        title="Tickets"
        subtitle="Centralized view of all YouTrack issues raised from Rexus across your projects."
        actions={
          <div className="flex flex-wrap items-center gap-2">
            {status?.base_url && (
              <a href={`${status.base_url.replace(/\/$/, '')}/issues`} target="_blank" rel="noopener noreferrer">
                <Button variant="ghost">
                  Open YouTrack <NavIcon name="external" size={14} />
                </Button>
              </a>
            )}
            <Button variant="secondary" disabled={syncing} onClick={syncNow}>
              <span className={cx('inline-flex', syncing && 'animate-spin')}>
                <NavIcon name="refresh" size={15} />
              </span>
              {syncing ? 'Syncing…' : 'Sync status'}
            </Button>
            {writable && projects.length > 0 && (
              <Button onClick={() => setCreating(true)}>New issue</Button>
            )}
          </div>
        }
      />

      {status && !status.configured && (
        <div className="mb-5 rounded-md border border-amber-900 bg-amber-950/30 px-4 py-3 text-sm text-amber-200">
          YouTrack is not connected yet.{' '}
          {admin ? (
            <>
              Add a YouTrack channel under{' '}
              <Link to="/alerts?view=channels" className="font-medium underline underline-offset-2">
                Alerts → Channels
              </Link>{' '}
              or set <code>REXUS_YOUTRACK_URL</code> and <code>REXUS_YOUTRACK_TOKEN</code> in <code>.env</code>.
            </>
          ) : (
            'Ask an administrator to configure YouTrack under Alerts → Channels.'
          )}
        </div>
      )}

      <div className="mb-5 grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Card className="px-4 py-3">
          <div className="text-xs font-medium text-zinc-400">Open tickets</div>
          <div className={cx('mt-1 text-xl font-semibold tabular-nums', openLinks.length > 0 ? 'text-sky-300' : 'text-zinc-50')}>
            {openLinks.length}
          </div>
          <div className="mt-0.5 text-xs text-zinc-500">Unresolved in YouTrack</div>
        </Card>
        <Card className="px-4 py-3">
          <div className="text-xs font-medium text-zinc-400">High priority</div>
          <div className={cx('mt-1 text-xl font-semibold tabular-nums', highPriorityOpen.length > 0 ? 'text-red-300' : 'text-zinc-50')}>
            {highPriorityOpen.length}
          </div>
          <div className="mt-0.5 text-xs text-zinc-500">Critical &amp; Major open</div>
        </Card>
        <Card className="px-4 py-3">
          <div className="text-xs font-medium text-zinc-400">Auto-raised</div>
          <div className="mt-1 text-xl font-semibold tabular-nums text-zinc-50">{autoRaisedOpen.length}</div>
          <div className="mt-0.5 text-xs text-zinc-500">From checks, jobs &amp; expiring keys</div>
        </Card>
        <Card className="px-4 py-3">
          <div className="text-xs font-medium text-zinc-400">Resolved</div>
          <div className="mt-1 text-xl font-semibold tabular-nums text-emerald-400">{resolvedLinks.length}</div>
          <div className="mt-0.5 text-xs text-zinc-500">Auto-resolved or closed</div>
        </Card>
      </div>

      <div className="mb-4 flex flex-wrap items-center gap-2">
        <div className="relative min-w-0 flex-[1_1_14rem] sm:max-w-xs">
          <span className="pointer-events-none absolute inset-y-0 left-2.5 flex items-center text-zinc-500">
            <NavIcon name="search" size={16} />
          </span>
          <Input
            value={q}
            onChange={(e) => setParam('q', e.target.value)}
            placeholder="Search ID, summary, project, assignee…"
            aria-label="Search tickets"
            className="pl-8"
          />
        </div>
        <Select
          value={stateFilter}
          onChange={(e) => setParam('state', e.target.value === 'open' ? '' : e.target.value)}
          aria-label="Filter by status"
          className="w-auto!"
        >
          <option value="open">Open ({openLinks.length})</option>
          <option value="resolved">Resolved ({resolvedLinks.length})</option>
          <option value="all">All tickets ({allLinks.length})</option>
        </Select>
        {projects.length > 1 && (
          <Select
            value={projectFilter}
            onChange={(e) => setParam('project', e.target.value)}
            aria-label="Filter by project"
            className="w-auto!"
          >
            <option value="">All projects</option>
            {[...projects]
              .sort((a, b) => a.name.localeCompare(b.name))
              .map((p) => (
                <option key={p.id} value={p.key}>
                  {p.name}
                </option>
              ))}
          </Select>
        )}
        <Select
          value={sourceFilter}
          onChange={(e) => setParam('source', e.target.value)}
          aria-label="Filter by source"
          className="w-auto!"
        >
          <option value="">All sources</option>
          <option value="check">Health check</option>
          <option value="job">Job</option>
          <option value="secret">Expiring secret</option>
          <option value="ssl">SSL certificate</option>
          <option value="manual">Manual</option>
        </Select>
        {filtered && (
          <button type="button" onClick={clearFilters} className="text-sm text-zinc-400 hover:text-zinc-200 hover:underline">
            Clear filters
          </button>
        )}
      </div>

      <ErrorBox error={error} />

      {allLinks.length === 0 ? (
        <Empty title="No tickets raised from Rexus yet">
          When a health check goes down, a job fails, or a secret is about to expire, Rexus automatically creates and tracks the YouTrack ticket here — or click <b>New issue</b> to raise one manually.
        </Empty>
      ) : shown.length === 0 ? (
        <Empty title={stateFilter === 'open' && openLinks.length === 0 ? 'All raised tickets are resolved' : 'No tickets match these filters'}>
          {stateFilter === 'open' && resolvedLinks.length > 0 ? (
            <button type="button" onClick={() => setParam('state', 'all')} className="text-emerald-400 hover:underline">
              Show all {allLinks.length} tickets (including resolved)
            </button>
          ) : (
            <button type="button" onClick={clearFilters} className="text-emerald-400 hover:underline">
              Clear filters
            </button>
          )}
        </Empty>
      ) : (
        <Card className="overflow-x-auto">
          <table className="w-full min-w-[760px] text-left text-sm">
            <thead className="border-b border-zinc-800 text-xs text-zinc-500">
              <tr>
                <th className="px-4 py-2.5 font-medium">Ticket</th>
                <th className="px-4 py-2.5 font-medium">Triggered by</th>
                <th className="px-4 py-2.5 font-medium">Project</th>
                <th className="px-4 py-2.5 font-medium">State</th>
                <th className="px-4 py-2.5 font-medium">Priority</th>
                <th className="px-4 py-2.5 font-medium">Assignee</th>
                <th className="px-4 py-2.5 text-right font-medium">Updated</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-zinc-800/70">
              {shown.map((l) => (
                <tr key={l.id} className="hover:bg-zinc-800/30">
                  <td className="max-w-md px-4 py-2.5">
                    <a
                      href={l.issue_url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="group flex items-center gap-2"
                    >
                      <span
                        className={cx(
                          'mono shrink-0 text-xs font-semibold',
                          l.resolved ? 'text-zinc-500 line-through' : 'text-sky-400 group-hover:underline',
                        )}
                      >
                        {l.issue_readable}
                      </span>
                      <span className={cx('truncate font-medium', l.resolved ? 'text-zinc-500' : 'text-zinc-100 group-hover:underline')}>
                        {l.summary}
                      </span>
                      <span className="shrink-0 text-zinc-500 opacity-0 transition group-hover:opacity-100">
                        <NavIcon name="external" size={12} />
                      </span>
                    </a>
                  </td>
                  <td className="px-4 py-2.5">
                    <div className="flex flex-wrap items-center gap-1.5">
                      <Badge tone={l.resource_type === 'manual' ? 'zinc' : 'sky'}>{SOURCE_LABEL[l.resource_type]}</Badge>
                      {l.resource_name && (
                        <Link to={sourceLink(l)} className="truncate text-xs text-zinc-300 hover:text-zinc-100 hover:underline">
                          {l.resource_name}
                        </Link>
                      )}
                    </div>
                  </td>
                  <td className="px-4 py-2.5">
                    <div className="flex items-center gap-1.5">
                      <Link to={`/projects/${l.project_key}`} className="text-zinc-300 hover:text-zinc-100 hover:underline">
                        {l.project_name || l.project_key}
                      </Link>
                      <span className="mono text-xs text-zinc-500">({l.yt_project_key})</span>
                    </div>
                  </td>
                  <td className="px-4 py-2.5">
                    <Badge tone={stateTone(l.state, l.resolved)}>{l.state || (l.resolved ? 'Done' : 'Open')}</Badge>
                  </td>
                  <td className="px-4 py-2.5">
                    {l.priority ? <Badge tone={priorityTone(l.priority)}>{l.priority}</Badge> : <span className="text-xs text-zinc-600">—</span>}
                  </td>
                  <td className="px-4 py-2.5 text-xs text-zinc-300">
                    {l.assignee ? `@${l.assignee}` : <span className="text-zinc-600">Unassigned</span>}
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5 text-right text-xs text-zinc-500" title={`Created ${fmtTime(l.created_at)} · Updated ${fmtTime(l.updated_at)}`}>
                    {timeAgo(l.updated_at)}
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
            projectKey: projectFilter || '',
            projects,
            summary: '',
            description: '',
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
    </>
  )
}
