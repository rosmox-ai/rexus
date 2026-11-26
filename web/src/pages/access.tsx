import { useCallback, useEffect, useState } from 'react'
import { api, type Project, type User } from '../api'
import { Badge, Button, Card, ErrorBox, Modal, Spinner, cx } from '../ui'

// Project access (plan.md Phase 10): who can see which project.

/** The projects the signed-in person can see — what they can hand out. */
export function useVisibleProjects() {
  const [projects, setProjects] = useState<Project[] | null>(null)
  useEffect(() => {
    api<{ projects: Project[] }>('/api/projects')
      .then((r) => setProjects(r.projects))
      .catch(() => setProjects([]))
  }, [])
  return projects
}

/** A checklist of projects, with an optional per-project Vault access checkbox. */
export function ProjectPicker({
  projects,
  value,
  onChange,
  vaultValue,
  onVaultChange,
  disabled,
}: {
  projects: Project[]
  value: string[]
  onChange: (keys: string[]) => void
  vaultValue?: string[]
  onVaultChange?: (keys: string[]) => void
  disabled?: boolean
}) {
  if (projects.length === 0) return <p className="text-sm text-zinc-500">There are no projects you can share yet.</p>
  const showVault = vaultValue !== undefined && onVaultChange !== undefined
  const toggle = (k: string) => {
    const next = value.includes(k) ? value.filter((x) => x !== k) : [...value, k]
    onChange(next)
    if (showVault && !next.includes(k) && vaultValue.includes(k)) {
      onVaultChange(vaultValue.filter((x) => x !== k))
    }
  }
  const toggleVault = (k: string) => {
    if (!showVault) return
    onVaultChange(vaultValue.includes(k) ? vaultValue.filter((x) => x !== k) : [...vaultValue, k])
  }
  return (
    <div className={cx('max-h-56 overflow-y-auto rounded-md border border-zinc-800 divide-y divide-zinc-800/70', disabled && 'opacity-50')}>
      {projects.map((p) => {
        const checked = value.includes(p.key)
        const vaultChecked = showVault && vaultValue.includes(p.key)
        const canToggleVault = checked && p.vault_access && !disabled
        return (
          <div key={p.id} className="flex items-center justify-between gap-3 px-3 py-2 text-sm hover:bg-zinc-900/60">
            <label className="flex min-w-0 flex-1 cursor-pointer items-center gap-3">
              <input type="checkbox" className="accent-emerald-500" checked={checked} disabled={disabled} onChange={() => toggle(p.key)} />
              <span className="truncate text-zinc-100">{p.name}</span>
              <span className="mono text-xs text-zinc-500">{p.key}</span>
              {p.owner_only && <Badge tone="amber">owner only</Badge>}
            </label>
            {showVault && (
              <label
                className={cx('flex shrink-0 items-center gap-1.5 text-xs', canToggleVault ? 'cursor-pointer text-zinc-300' : 'cursor-not-allowed text-zinc-600')}
                title={!p.vault_access ? "You don't have vault access to this project" : !checked ? 'Enable project access first' : 'Allow access to vault secrets'}
              >
                <input
                  type="checkbox"
                  className="accent-emerald-500"
                  checked={vaultChecked}
                  disabled={!canToggleVault}
                  onChange={() => toggleVault(p.key)}
                />
                <span>Vault</span>
              </label>
            )}
          </div>
        )
      })}
    </div>
  )
}

/**
 * What a new or existing user can see. Admins get a choice between every project and chosen ones
 * (only the owner makes it); contributors and viewers always get chosen ones.
 */
export function AccessChoice({
  role,
  canSetAll,
  projects,
  allProjects,
  allVault,
  keys,
  vaultKeys,
  onChange,
}: {
  role: string
  canSetAll: boolean
  projects: Project[]
  allProjects: boolean
  allVault: boolean
  keys: string[]
  vaultKeys: string[]
  onChange: (allProjects: boolean, allVault: boolean, keys: string[], vaultKeys: string[]) => void
}) {
  const isAdminRole = role === 'admin'
  return (
    <div className="space-y-2">
      {isAdminRole && canSetAll && (
        <div className="flex gap-4 text-sm">
          <label className="flex items-center gap-2 text-zinc-300">
            <input type="radio" className="accent-emerald-500" checked={allProjects} onChange={() => onChange(true, allVault, keys, vaultKeys)} /> All projects
          </label>
          <label className="flex items-center gap-2 text-zinc-300">
            <input type="radio" className="accent-emerald-500" checked={!allProjects} onChange={() => onChange(false, false, keys, vaultKeys)} /> Selected projects
          </label>
        </div>
      )}
      {isAdminRole && allProjects ? (
        <div className="space-y-2">
          <p className="text-xs text-zinc-500">Sees every project except ones you mark “owner only”. New projects appear for them automatically.</p>
          {canSetAll && (
            <label className="flex cursor-pointer items-center gap-2 text-sm text-zinc-300">
              <input
                type="checkbox"
                className="accent-emerald-500"
                checked={allVault}
                onChange={(e) => onChange(true, e.target.checked, keys, vaultKeys)}
              />
              <span>Vault access for all projects</span>
            </label>
          )}
        </div>
      ) : (
        <ProjectPicker
          projects={projects}
          value={keys}
          onChange={(k) => onChange(false, false, k, vaultKeys.filter((v) => k.includes(v)))}
          vaultValue={vaultKeys}
          onVaultChange={(vk) => onChange(false, false, keys, vk)}
        />
      )}
      {isAdminRole && !allProjects && <p className="text-xs text-zinc-500">An admin always sees the projects they create themselves.</p>}
    </div>
  )
}

/** Edit what one user can see (Users page). */
export function UserAccessModal({
  user,
  me,
  grants,
  vaultGrants,
  onClose,
  onSaved,
}: {
  user: User
  me: User
  grants: string[]
  vaultGrants: string[]
  onClose: () => void
  onSaved: () => void
}) {
  const projects = useVisibleProjects()
  const [all, setAll] = useState(user.all_projects)
  const [allVault, setAllVault] = useState(user.all_vault)
  const [keys, setKeys] = useState(grants)
  const [vaultKeys, setVaultKeys] = useState(vaultGrants)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const ownerEditing = me.role === 'owner'

  const save = async () => {
    setBusy(true)
    setError(null)
    try {
      const body: Record<string, unknown> = {}
      if (user.role === 'admin' && ownerEditing) {
        if (all !== user.all_projects) body.all_projects = all
        if ((all ? allVault : false) !== user.all_vault) body.all_vault = all && allVault
      }
      if (!(user.role === 'admin' && all)) {
        body.projects = keys
        // Only send vault_projects for projects the actor can manage vault access on.
        const manageableVault = new Set((projects ?? []).filter((p) => p.vault_access).map((p) => p.key))
        body.vault_projects = vaultKeys.filter((k) => keys.includes(k) && manageableVault.has(k))
      }
      if (Object.keys(body).length === 0) {
        onClose()
        return
      }
      await api(`/api/users/${user.id}`, { method: 'PATCH', body })
      onSaved()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={`What ${user.name || user.email} can see`} onClose={onClose}>
      <div className="space-y-4">
        {!projects ? (
          <Spinner />
        ) : (
          <AccessChoice
            role={user.role}
            canSetAll={ownerEditing}
            projects={projects}
            allProjects={all}
            allVault={allVault}
            keys={keys}
            vaultKeys={vaultKeys}
            onChange={(a, av, k, vk) => {
              setAll(a)
              setAllVault(av)
              setKeys(k)
              setVaultKeys(vk)
            }}
          />
        )}
        {me.role !== 'owner' && <p className="text-xs text-zinc-500">You can only share projects you can see yourself (and vault access only where you have it). Other access stays as it is.</p>}
        <ErrorBox error={error} />
        <div className="flex gap-2">
          <Button onClick={save} disabled={busy || !projects}>{busy ? 'Saving…' : 'Save'}</Button>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
        </div>
      </div>
    </Modal>
  )
}

// ───────────────────────── project page: Access section ─────────────────────────

interface Person { id: string; email: string; name: string; role: string; via: 'owner' | 'all_projects' | 'creator' | 'shared'; vault_access: boolean }
interface ShareUser { id: string; email: string; name: string; role: string; shared: boolean; vault_access: boolean; manageable: boolean }
interface AccessInfo { owner_only: boolean; can_set_owner_only: boolean; can_manage_vault: boolean; people: Person[]; users: ShareUser[] }

const VIA: Record<Person['via'], string> = { owner: 'owner', all_projects: 'all projects', creator: 'created it', shared: 'shared' }

/** The project's Access dialog, opened from the project's ⋯ menu. */
export function ProjectAccessModal({ project, onClose, onChanged }: { project: Project; onClose: () => void; onChanged: () => void }) {
  return (
    <Modal title={`Access — ${project.name}`} onClose={onClose} wide>
      <ProjectAccess project={project} onChanged={onChanged} />
    </Modal>
  )
}

function ProjectAccess({ project, onChanged }: { project: Project; onChanged: () => void }) {
  const [info, setInfo] = useState<AccessInfo | null>(null)
  const [chosen, setChosen] = useState<string[]>([])
  const [vaultChosen, setVaultChosen] = useState<string[]>([])
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [saved, setSaved] = useState(false)

  const load = useCallback(() => {
    api<AccessInfo>(`/api/projects/${encodeURIComponent(project.key)}/access`)
      .then((r) => {
        setInfo(r)
        setChosen(r.users.filter((u) => u.shared).map((u) => u.id))
        setVaultChosen(r.users.filter((u) => u.shared && u.vault_access).map((u) => u.id))
      })
      .catch((e) => setError(e.message))
  }, [project.key])
  useEffect(load, [load])

  const save = async () => {
    setBusy(true)
    setError(null)
    setSaved(false)
    try {
      const body: Record<string, unknown> = { user_ids: chosen }
      if (info?.can_manage_vault) {
        body.vault_user_ids = vaultChosen.filter((id) => chosen.includes(id))
      }
      await api(`/api/projects/${encodeURIComponent(project.key)}/access`, { method: 'PUT', body })
      setSaved(true)
      load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const setOwnerOnly = async (v: boolean) => {
    if (v && !confirm('Hide this project from every admin? Admins lose access immediately, unless it is shared with them by name.')) return
    setError(null)
    try {
      await api(`/api/projects/${encodeURIComponent(project.key)}`, { method: 'PATCH', body: { owner_only: v } })
      load()
      onChanged()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  if (!info) return error ? <ErrorBox error={error} /> : <Spinner />
  const shareable = info.users.filter((u) => u.manageable)
  const initialShared = info.users.filter((u) => u.shared).map((u) => u.id).sort()
  const initialVault = info.users.filter((u) => u.shared && u.vault_access).map((u) => u.id).sort()
  const dirty =
    JSON.stringify([...chosen].sort()) !== JSON.stringify(initialShared) ||
    (info.can_manage_vault && JSON.stringify([...vaultChosen].sort()) !== JSON.stringify(initialVault))

  const toggleUser = (id: string) => {
    if (chosen.includes(id)) {
      setChosen(chosen.filter((x) => x !== id))
      setVaultChosen(vaultChosen.filter((x) => x !== id))
    } else {
      setChosen([...chosen, id])
    }
  }
  const toggleVault = (id: string) => {
    setVaultChosen(vaultChosen.includes(id) ? vaultChosen.filter((x) => x !== id) : [...vaultChosen, id])
  }

  return (
    <div>
      <p className="mb-3 text-sm text-zinc-500">Who can see this project and its vault secrets. Contributors and viewers only see projects shared with them.</p>
      <ErrorBox error={error} />

      <div className="grid gap-4 md:grid-cols-2">
        <Card className="p-4">
          <div className="mb-2 text-xs font-medium uppercase tracking-wide text-zinc-500">Can see it now</div>
          <ul className="space-y-1.5">
            {info.people.map((p) => (
              <li key={p.id} className="flex items-center justify-between gap-3 text-sm">
                <span className="min-w-0 truncate"><span className="text-zinc-100">{p.name || p.email}</span> <span className="text-xs text-zinc-500">{p.email}</span></span>
                <span className="flex shrink-0 items-center gap-1.5">
                  {p.vault_access && <Badge tone="green">vault</Badge>}
                  <Badge tone={p.role === 'owner' ? 'green' : p.role === 'admin' ? 'sky' : 'zinc'}>{p.role}</Badge>
                  <span className="text-xs text-zinc-500">{VIA[p.via]}</span>
                </span>
              </li>
            ))}
          </ul>
        </Card>

        <Card className="p-4">
          <div className="mb-2 text-xs font-medium uppercase tracking-wide text-zinc-500">Share with</div>
          {shareable.length === 0 ? (
            <p className="text-sm text-zinc-500">There is nobody to share with yet — invite people on the Users page.</p>
          ) : (
            <div className="max-h-56 divide-y divide-zinc-800/70 overflow-y-auto rounded-md border border-zinc-800">
              {shareable.map((u) => {
                const isShared = chosen.includes(u.id)
                const isVault = vaultChosen.includes(u.id)
                const canToggleVault = isShared && info.can_manage_vault
                return (
                  <div key={u.id} className="flex items-center gap-3 px-3 py-2 text-sm hover:bg-zinc-900/60">
                    <label className="flex min-w-0 flex-1 cursor-pointer items-center gap-3">
                      <input type="checkbox" className="accent-emerald-500" checked={isShared} onChange={() => toggleUser(u.id)} />
                      <span className="min-w-0 truncate"><span className="text-zinc-100">{u.name || u.email}</span> <span className="text-xs text-zinc-500">{u.email}</span></span>
                    </label>
                    <label
                      className={cx('flex shrink-0 items-center gap-1.5 text-xs', canToggleVault ? 'cursor-pointer text-zinc-300' : 'cursor-not-allowed text-zinc-600')}
                      title={!info.can_manage_vault ? "You don't have vault access to this project" : !isShared ? 'Share project first' : 'Allow access to vault secrets'}
                    >
                      <input type="checkbox" className="accent-emerald-500" checked={isVault} disabled={!canToggleVault} onChange={() => toggleVault(u.id)} />
                      <span>Vault</span>
                    </label>
                    <span className="shrink-0"><Badge tone={u.role === 'admin' ? 'sky' : 'zinc'}>{u.role}</Badge></span>
                  </div>
                )
              })}
            </div>
          )}
          <div className="mt-3 flex items-center gap-3">
            <Button onClick={save} disabled={busy || !dirty || shareable.length === 0}>{busy ? 'Saving…' : 'Save access'}</Button>
            {saved && !dirty && <span className="text-xs text-emerald-400">Saved</span>}
          </div>
        </Card>
      </div>

      {info.can_set_owner_only && (
        <Card className="mt-4 flex items-center justify-between gap-4 p-4">
          <div className="text-sm">
            <div className="font-medium text-zinc-200">Owner only</div>
            <div className="text-zinc-500">Hide this project from every admin — even ones with access to all projects — unless you share it with them by name.</div>
          </div>
          <Button className="shrink-0 whitespace-nowrap" variant={info.owner_only ? 'danger' : 'secondary'} onClick={() => setOwnerOnly(!info.owner_only)}>
            {info.owner_only ? 'Owner only — turn off' : 'Make owner only'}
          </Button>
        </Card>
      )}
    </div>
  )
}
