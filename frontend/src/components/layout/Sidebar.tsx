import { useState, useEffect } from 'react'
import { useAgents } from '../../hooks/use-agents'
import { useSessions } from '../../hooks/use-sessions'
import { useProjects } from '../../hooks/use-projects'
import { useUiStore } from '../../stores/ui-store'
import { clsx } from 'clsx'
import { AgentTagBadge } from '../agents/AgentTagBadge'
import { SelectDirectory } from '../../../bindings/github.com/zboya/nurvis/cmd/nurvis-desktop/service'

// Wails3 desktop binding (only effective in desktop environment)
function isWailsRuntime(): boolean {
  return typeof window !== 'undefined' && '_wails' in window
}

function formatTime(ts: number): string {
  const d = new Date(ts)
  const now = new Date()
  if (d.toDateString() === now.toDateString()) {
    return d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
  }
  return d.toLocaleDateString('zh-CN', { month: 'short', day: 'numeric' })
}

// ── Persisted collapse state ────────────────────────────────────────────────────────────

function useSidebarCollapse(id: string): [boolean, () => void] {
  const key = 'nurvis:sidebar:collapsed:' + id
  const [collapsed, setCollapsed] = useState<boolean>(() => {
    try { return localStorage.getItem(key) === '1' } catch { return false }
  })
  const toggle = () => setCollapsed((v) => {
    const next = !v
    try { next ? localStorage.setItem(key, '1') : localStorage.removeItem(key) } catch { /* ignore */ }
    return next
  })
  return [collapsed, toggle]
}

// ── SidebarSectionHeader ──────────────────────────────────────────────────────

function SidebarSectionHeader({ title, collapsed, onToggle, action }: {
  title: string
  collapsed: boolean
  onToggle: () => void
  action?: React.ReactNode
}) {
  return (
    <div className="flex items-center justify-between px-1">
      <button
        type="button"
        onClick={onToggle}
        className="flex items-center gap-1 min-w-0 text-left text-text-muted hover:text-text-secondary transition-colors"
      >
        <svg
          width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor"
          strokeWidth={2.5} strokeLinecap="round" strokeLinejoin="round"
          className={clsx('shrink-0 transition-transform', collapsed ? '-rotate-90' : '')}
        >
          <polyline points="6 9 12 15 18 9" />
        </svg>
        <span className="text-xs font-semibold tracking-wide truncate">{title}</span>
      </button>
      {action && <div className="ml-auto">{action}</div>}
    </div>
  )
}

// ── AddProjectModal ───────────────────────────────────────────────────────────

function AddProjectModal({ onClose, onAdd }: {
  onClose: () => void
  onAdd: (name: string, dir: string, desc: string) => Promise<void>
}) {
  const [name, setName] = useState('')
  const [dir, setDir] = useState('')
  const [desc, setDesc] = useState('')
  const [loading, setLoading] = useState(false)

  const handleSubmit = async () => {
    if (!name.trim() || !dir.trim()) return
    setLoading(true)
    await onAdd(name.trim(), dir.trim(), desc.trim())
    setLoading(false)
    onClose()
  }

  const handleBrowse = async () => {
    try {
      const selected = await SelectDirectory()
      if (selected) setDir(selected)
    } catch {
      // User cancelled or error; ignore
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-sm">
      <div className="w-80 bg-surface-secondary border border-border rounded-2xl shadow-xl p-5 space-y-3">
        <p className="text-sm font-semibold text-text-primary">添加项目</p>

        <div className="space-y-2">
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="项目名称"
            autoFocus
            className="w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent/60 transition-colors"
          />
          <div className="flex items-center gap-1.5">
            <input
              value={dir}
              onChange={(e) => setDir(e.target.value)}
              placeholder="本地目录路径，例：/Users/me/myproject"
              className="flex-1 min-w-0 bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent/60 transition-colors font-mono"
            />
            {isWailsRuntime() && (
              <button
                type="button"
                onClick={handleBrowse}
                title="选择目录"
                className="shrink-0 flex items-center justify-center w-8 h-8 bg-surface-tertiary border border-border/60 rounded-lg text-text-muted hover:text-text-primary hover:border-accent/60 transition-colors"
              >
                <svg className="w-4 h-4" fill="none" stroke="currentColor" strokeWidth={1.8} viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v9a2 2 0 01-2 2H5a2 2 0 01-2-2V7z" />
                </svg>
              </button>
            )}
          </div>
          <input
            value={desc}
            onChange={(e) => setDesc(e.target.value)}
            placeholder="描述（可选）"
            className="w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent/60 transition-colors"
          />
        </div>

        <div className="flex gap-2 pt-1">
          <button
            onClick={handleSubmit}
            disabled={!name.trim() || !dir.trim() || loading}
            className="flex-1 py-2 bg-accent text-white text-sm font-medium rounded-lg disabled:opacity-40 hover:bg-accent-hover transition-colors"
          >
            {loading ? '添加中…' : '添加'}
          </button>
          <button
            onClick={onClose}
            className="flex-1 py-2 bg-surface-tertiary text-text-secondary text-sm rounded-lg hover:text-text-primary transition-colors"
          >
            取消
          </button>
        </div>
      </div>
    </div>
  )
}

// ── Sidebar ───────────────────────────────────────────────────────────────────

export function Sidebar() {
  const { agents } = useAgents()
  const { projects, createProject, deleteProject } = useProjects()
  const activeAgentId = useUiStore((s) => s.activeAgentId)
  const activeSessionId = useUiStore((s) => s.activeSessionId)
  const activeProjectId = useUiStore((s) => s.activeProjectId)
  const setActiveAgent = useUiStore((s) => s.setActiveAgent)
  const setActiveSession = useUiStore((s) => s.setActiveSession)
  const setActiveProject = useUiStore((s) => s.setActiveProject)
  const incrementSessionResetKey = useUiStore((s) => s.incrementSessionResetKey)
  const setView = useUiStore((s) => s.setView)
  const view = useUiStore((s) => s.view)
  const { sessions } = useSessions(activeAgentId)

  const [agentsCollapsed, toggleAgentsCollapsed] = useSidebarCollapse('agents')
  const [projectsCollapsed, toggleProjectsCollapsed] = useSidebarCollapse('projects')
  const [projectSessionsCollapsed, setProjectSessionsCollapsed] = useState<Record<string, boolean>>({})
  const [showAddProject, setShowAddProject] = useState(false)
  const [projectMenuId, setProjectMenuId] = useState<string | null>(null)

  // Auto-select the first agent
  useEffect(() => {
    if (!activeAgentId && agents.length > 0) {
      setActiveAgent(agents[0].id)
      if (view !== 'chat') setView('chat')
    }
  }, [agents, activeAgentId, setActiveAgent, view, setView])

  const handleAddProject = async (name: string, dir: string, desc: string) => {
    await createProject(name, dir, desc)
  }

  const handleSelectProject = (id: string) => {
    if (activeProjectId === id) {
      setActiveProject(null)
    } else {
      setActiveProject(id)
      setActiveSession(null)
    }
    setProjectMenuId(null)
  }

  const handleDeleteProject = async (id: string) => {
    if (activeProjectId === id) setActiveProject(null)
    await deleteProject(id)
    setProjectMenuId(null)
  }

  const toggleProjectSessions = (projectId: string) => {
    setProjectSessionsCollapsed((prev) => ({ ...prev, [projectId]: !prev[projectId] }))
  }

  // Sessions grouped by project
  const projectSessions = (projectId: string) =>
    sessions.filter((s) => s.project_id === projectId)

  // Sessions without a project
  const unassignedSessions = sessions.filter((s) => !s.project_id)

  return (
    <>
      <div className="w-56 flex flex-col bg-sidebar border-r border-border h-full shrink-0">
        {/* Logo — placeholder only, reserves space for traffic-light area */}
        <div className="h-8 shrink-0" />

        {/* Scrollable content */}
        <div className="flex-1 flex flex-col overflow-hidden">

          {/* App name */}
          <div className="px-4 pb-1 shrink-0">
            <span className="text-sm font-semibold text-text-primary">Nurvis</span>
          </div>

          {/* ── Agents section ── */}
          <div className="px-3 pt-1 pb-1 shrink-0">
            <SidebarSectionHeader
              title="智能体"
              collapsed={agentsCollapsed}
              onToggle={toggleAgentsCollapsed}
            />
          </div>

          {!agentsCollapsed && (
            <div className="px-2 pb-1 space-y-0.5 shrink-0">
              {agents.map((a) => {
                const ae = a as { id: string; name: string; emoji?: string }
                return (
                  <div key={a.id} className="relative group">
                    <button
                      onClick={() => {
                        setActiveAgent(a.id)
                        setActiveSession(null)
                        incrementSessionResetKey()
                        if (view !== 'chat') setView('chat')
                      }}
                      className={clsx(
                        'w-full flex items-center gap-2 px-2 py-1.5 rounded-lg text-left transition-all pr-8',
                        activeAgentId === a.id && view === 'chat'
                          ? 'bg-accent/15 text-accent'
                          : 'text-text-secondary hover:bg-surface-tertiary hover:text-text-primary'
                      )}
                    >
                      <span className="text-sm shrink-0">{ae.emoji ?? '🤖'}</span>
                      <span className="text-xs font-medium truncate">{a.name}</span>
                      <AgentTagBadge tag={a.tag} size="xs" className="shrink-0" />
                    </button>
                    {/* New session button */}
                    <button
                      onClick={(e) => {
                        e.stopPropagation()
                        setActiveAgent(a.id)
                        setActiveSession(null)
                        incrementSessionResetKey()
                        if (view !== 'chat') setView('chat')
                      }}
                      title="新建会话"
                      className="absolute right-1 top-1/2 -translate-y-1/2 opacity-0 group-hover:opacity-100 w-5 h-5 flex items-center justify-center rounded text-text-muted hover:text-accent hover:bg-surface-tertiary transition-all"
                    >
                      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2.5} strokeLinecap="round" strokeLinejoin="round">
                        <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
                        <line x1="12" y1="9" x2="12" y2="13" /><line x1="10" y1="11" x2="14" y2="11" />
                      </svg>
                    </button>
                  </div>
                )
              })}
              {agents.length === 0 && (
                <p className="text-2xs text-text-muted px-2 py-1">暂无智能体</p>
              )}
            </div>
          )}

          {/* ── Projects section ── */}
          <div className="px-3 pt-2 pb-1 shrink-0">
            <SidebarSectionHeader
              title="项目"
              collapsed={projectsCollapsed}
              onToggle={toggleProjectsCollapsed}
              action={
                <button
                  onClick={() => setShowAddProject(true)}
                  className="text-text-muted hover:text-text-secondary transition-colors p-0.5 rounded"
                  title="添加项目"
                >
                  <svg className="w-3 h-3" fill="none" stroke="currentColor" strokeWidth={2.5} viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M12 4v16m8-8H4" />
                  </svg>
                </button>
              }
            />
          </div>

          {!projectsCollapsed && (
            <div className="px-2 pb-1 space-y-0.5 shrink-0">
              {projects.map((p) => {
                const pSessions = projectSessions(p.id)
                const isActive = activeProjectId === p.id
                const isCollapsed = projectSessionsCollapsed[p.id] ?? false
                return (
                  <div key={p.id}>
                    <div className="relative group">
                      <button
                        onClick={() => handleSelectProject(p.id)}
                        className={clsx(
                          'w-full flex items-center gap-1.5 px-2 py-1.5 rounded-lg text-left transition-all pr-12',
                          isActive
                            ? 'bg-accent/10 text-accent'
                            : 'text-text-secondary hover:bg-surface-tertiary hover:text-text-primary'
                        )}
                      >
                        {/* Collapse/expand arrow (shown when selected and has sessions) */}
                        {isActive && pSessions.length > 0 ? (
                          <span
                            onClick={(e) => { e.stopPropagation(); toggleProjectSessions(p.id) }}
                            className="w-4 h-4 flex items-center justify-center shrink-0 rounded hover:bg-surface-tertiary"
                          >
                            <svg
                              width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                              strokeWidth={2.5} strokeLinecap="round" strokeLinejoin="round"
                              className={clsx('transition-transform', isCollapsed ? '-rotate-90' : '')}
                            >
                              <polyline points="6 9 12 15 18 9" />
                            </svg>
                          </span>
                        ) : (
                          <span className="w-4 h-4 flex items-center justify-center shrink-0 text-xs">📁</span>
                        )}
                        <div className="min-w-0">
                          <p className="text-xs font-medium truncate">{p.name}</p>
                          <p className="text-2xs text-text-muted truncate font-mono">{p.dir.split('/').pop()}</p>
                        </div>
                      </button>
                      {/* New session button */}
                      <button
                        onClick={(e) => {
                          e.stopPropagation()
                          setActiveProject(p.id)
                          setActiveSession(null)
                          incrementSessionResetKey()
                          if (view !== 'chat') setView('chat')
                        }}
                        title="在此项目新建会话"
                        className="absolute right-6 top-1/2 -translate-y-1/2 opacity-0 group-hover:opacity-100 w-5 h-5 flex items-center justify-center rounded text-text-muted hover:text-accent hover:bg-surface-tertiary transition-all"
                      >
                        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2.5} strokeLinecap="round" strokeLinejoin="round">
                          <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
                          <line x1="12" y1="9" x2="12" y2="13" /><line x1="10" y1="11" x2="14" y2="11" />
                        </svg>
                      </button>
                      {/* Delete button */}
                      <button
                        onClick={(e) => { e.stopPropagation(); handleDeleteProject(p.id) }}
                        title="删除项目"
                        className="absolute right-1 top-1/2 -translate-y-1/2 opacity-0 group-hover:opacity-100 w-5 h-5 flex items-center justify-center rounded text-text-muted hover:text-error hover:bg-surface-tertiary transition-all"
                      >
                        <svg className="w-3 h-3" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
                          <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
                        </svg>
                      </button>
                    </div>

                    {/* Session list under project */}
                    {isActive && !isCollapsed && pSessions.length > 0 && (
                      <div className="ml-4 mt-0.5 space-y-0.5 border-l border-border/40 pl-2">
                        {pSessions.map((s) => (
                          <button
                            key={s.id}
                            onClick={() => setActiveSession(s.id)}
                            className={clsx(
                              'w-full flex flex-col items-start px-2 py-1 rounded-md text-left transition-all',
                              activeSessionId === s.id
                                ? 'bg-surface-tertiary text-text-primary'
                                : 'text-text-muted hover:bg-surface-tertiary/60 hover:text-text-secondary'
                            )}
                          >
                            <span className="text-2xs truncate w-full">
                              {s.label ?? s.summary ?? '新对话'}
                            </span>
                            <span className="text-2xs text-text-muted/70 mt-0.5">
                              {formatTime(s.updated_at)}
                            </span>
                          </button>
                        ))}
                      </div>
                    )}
                  </div>
                )
              })}

              {projects.length === 0 && (
                <p className="text-2xs text-text-muted px-2 py-1">点击 + 添加本地目录</p>
              )}
            </div>
          )}

          {/* ── Chat history (sessions without project) ── */}
          {activeAgentId && (
            <>
              <div className="px-3 py-1.5 flex items-center justify-between shrink-0">
                <span className="text-2xs text-text-muted font-medium uppercase tracking-wider">
                  对话历史
                </span>
                <span className="text-2xs text-text-muted">{unassignedSessions.length}</span>
              </div>
              <div className="flex-1 overflow-y-auto px-2 space-y-0.5 pb-2">
                {unassignedSessions.map((s) => (
                  <button
                    key={s.id}
                    onClick={() => setActiveSession(s.id)}
                    className={clsx(
                      'w-full flex flex-col items-start px-2 py-1 rounded-lg text-left transition-all',
                      activeSessionId === s.id
                        ? 'bg-surface-tertiary text-text-primary'
                        : 'text-text-muted hover:bg-surface-tertiary/60 hover:text-text-secondary'
                    )}
                  >
                    <span className="text-2xs truncate w-full">
                      {s.label ?? s.summary ?? '新对话'}
                    </span>
                    <span className="text-2xs text-text-muted/70 mt-0.5">
                      {formatTime(s.updated_at)}
                    </span>
                  </button>
                ))}
                {unassignedSessions.length === 0 && (
                  <p className="text-2xs text-text-muted px-2 py-2">发送消息开始对话</p>
                )}
              </div>
            </>
          )}
        </div>

        {/* Bottom: settings button */}
        <div className="shrink-0 p-2">
          <button
            onClick={() => setView('settings')}
            className={clsx(
              'w-full flex items-center gap-2 px-2 py-2 rounded-lg text-xs font-medium transition-colors',
              view === 'settings'
                ? 'bg-accent/15 text-accent'
                : 'text-text-muted hover:bg-surface-tertiary hover:text-text-secondary'
            )}
          >
            <svg className="w-4 h-4 shrink-0" fill="none" stroke="currentColor" strokeWidth={1.8} viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" d="M9.594 3.94c.09-.542.56-.94 1.11-.94h2.593c.55 0 1.02.398 1.11.94l.213 1.281c.063.374.313.686.645.87.074.04.147.083.22.127.325.196.72.257 1.075.124l1.217-.456a1.125 1.125 0 011.37.49l1.296 2.247a1.125 1.125 0 01-.26 1.431l-1.003.827c-.293.241-.438.613-.43.992a7.723 7.723 0 010 .255c-.008.378.137.75.43.991l1.004.827c.424.35.534.955.26 1.43l-1.298 2.247a1.125 1.125 0 01-1.369.491l-1.217-.456c-.355-.133-.75-.072-1.076.124a6.47 6.47 0 01-.22.128c-.331.183-.581.495-.644.869l-.213 1.281c-.09.543-.56.94-1.11.94h-2.594c-.55 0-1.019-.398-1.11-.94l-.213-1.281c-.062-.374-.312-.686-.644-.87a6.52 6.52 0 01-.22-.127c-.325-.196-.72-.257-1.076-.124l-1.217.456a1.125 1.125 0 01-1.369-.49l-1.297-2.247a1.125 1.125 0 01.26-1.43l1.297-2.247a1.125 1.125 0 011.37-.491l1.216.456c.356.133.751.072 1.076-.124.072-.044.146-.086.22-.128.332-.183.582-.495.644-.869l.214-1.28z" />
              <path strokeLinecap="round" strokeLinejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
            </svg>
            设置
          </button>
        </div>
      </div>

      {showAddProject && (
        <AddProjectModal
          onClose={() => setShowAddProject(false)}
          onAdd={handleAddProject}
        />
      )}
    </>
  )
}