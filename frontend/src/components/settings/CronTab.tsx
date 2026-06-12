import { useState, useEffect } from 'react'
import { clsx } from 'clsx'
import { getWs } from '../../lib/ws'
import { useAgents } from '../../hooks/use-agents'
import { useProjects } from '../../hooks/use-projects'
import { SectionTitle, Card, Badge, Toggle } from './shared-ui'
import type { Channel, CronJob, CronRun } from './types'

interface CronFormState {
  name: string
  spec: string
  agent_id: string
  project_id: string
  prompt: string
  target_channel_id: string
  target_peer_id: string
  target_peer_type: 'user' | 'group'
}

const emptyCronForm: CronFormState = {
  name: '',
  spec: '0 0 9 * * *',
  agent_id: '',
  project_id: '',
  prompt: '',
  target_channel_id: '',
  target_peer_id: '',
  target_peer_type: 'user',
}

export function CronTab() {
  const ws = getWs()
  const { agents } = useAgents()
  const { projects } = useProjects()
  const [jobs, setJobs] = useState<CronJob[]>([])
  const [channels, setChannels] = useState<Channel[]>([])
  const [loading, setLoading] = useState(true)

  const [showForm, setShowForm] = useState(false)
  const [form, setForm] = useState<CronFormState>(emptyCronForm)
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const [confirmDel, setConfirmDel] = useState<string | null>(null)
  const [running, setRunning] = useState<string | null>(null)
  const [expanded, setExpanded] = useState<string | null>(null)
  const [runs, setRuns] = useState<Record<string, CronRun[]>>({})

  const load = () => {
    setLoading(true)
    ws.call<{ jobs: CronJob[] }>('cron.list')
      .then((r) => setJobs(r.jobs ?? []))
      .catch(() => {})
      .finally(() => setLoading(false))
  }
  useEffect(() => {
    load()
    // Fetch channel list once for form dropdown
    ws.call<{ channels: Channel[] }>('channels.list')
      .then((r) => setChannels(r.channels ?? []))
      .catch(() => {})
  }, [ws])

  const openNew = () => {
    setForm(emptyCronForm)
    setFormError(null)
    setShowForm(true)
  }

  const submit = async () => {
    if (!form.name.trim() || !form.spec.trim() || !form.agent_id || !form.prompt.trim()) {
      setFormError('请填写名称、cron 表达式、Agent 与提示词')
      return
    }
    if (form.target_peer_id.trim() && !form.target_channel_id) {
      setFormError('设置了对端 ID 时必须选择目标渠道')
      return
    }
    setSaving(true)
    setFormError(null)
    try {
      await ws.call('cron.create', {
        name: form.name.trim(),
        spec: form.spec.trim(),
        agent_id: form.agent_id,
        project_id: form.project_id || undefined,
        prompt: form.prompt.trim(),
        target_channel_id: form.target_channel_id || undefined,
        target_peer_id: form.target_peer_id.trim() || undefined,
        target_peer_type: form.target_peer_id.trim() ? form.target_peer_type : undefined,
      })
      setShowForm(false)
      load()
    } catch (e: any) {
      setFormError(e?.message ?? '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const toggle = async (id: string, enabled: boolean) => {
    await ws.call('cron.toggle', { id, enabled: !enabled }).catch(() => {})
    load()
  }

  const remove = async (id: string) => {
    if (confirmDel !== id) {
      setConfirmDel(id)
      setTimeout(() => setConfirmDel((cur) => (cur === id ? null : cur)), 3000)
      return
    }
    setConfirmDel(null)
    try {
      await ws.call('cron.delete', { id })
      load()
    } catch { /* ignore */ }
  }

  const runNow = async (id: string) => {
    setRunning(id)
    try {
      await ws.call('cron.run', { id })
      // Refresh history immediately
      loadRuns(id)
    } catch { /* ignore */ }
    setTimeout(() => setRunning((cur) => (cur === id ? null : cur)), 800)
  }

  const loadRuns = async (jobID: string) => {
    try {
      const r = await ws.call<{ runs: CronRun[] }>('cron.runs', { job_id: jobID, limit: 10 })
      setRuns((prev) => ({ ...prev, [jobID]: r.runs ?? [] }))
    } catch { /* ignore */ }
  }

  const toggleExpand = (id: string) => {
    if (expanded === id) {
      setExpanded(null)
      return
    }
    setExpanded(id)
    if (!runs[id]) loadRuns(id)
  }

  const fmtTs = (s?: string) => {
    if (!s) return '—'
    try {
      return new Date(s).toLocaleString()
    } catch {
      return s
    }
  }

  const channelDesc = (j: CronJob) => {
    if (!j.target_channel_id) return null
    const ch = channels.find((c) => c.id === j.target_channel_id)
    const channelLabel = ch ? `${ch.name}` : j.target_channel_id.slice(0, 8) + '…'
    if (!j.target_peer_id) return `→ ${channelLabel}`
    return `→ ${channelLabel} · ${j.target_peer_type || 'user'}:${j.target_peer_id}`
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <SectionTitle>定时任务</SectionTitle>
        <button
          onClick={openNew}
          disabled={agents.length === 0}
          className="text-xs px-3 py-1.5 bg-accent/15 text-accent rounded-lg hover:bg-accent/25 transition-colors disabled:opacity-40"
          title={agents.length === 0 ? '请先创建 Agent' : ''}
        >
          + 新建任务
        </button>
      </div>

      {showForm && (
        <Card>
          <div className="p-4 space-y-3">
            <p className="text-xs font-medium text-text-primary">新建定时任务</p>

            <div>
              <label className="text-2xs text-text-muted block mb-1">名称</label>
              <input
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                placeholder="例如：每天早 9 点天气播报"
                autoFocus
                className="w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent/60"
              />
            </div>

            <div>
              <label className="text-2xs text-text-muted block mb-1">
                Cron 表达式（6 段含秒，例：<code>0 0 9 * * *</code> = 每天 9:00）
              </label>
              <input
                value={form.spec}
                onChange={(e) => setForm({ ...form, spec: e.target.value })}
                placeholder="秒 分 时 日 月 周"
                className="w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent/60 font-mono"
              />
            </div>

            <div>
              <label className="text-2xs text-text-muted block mb-1">Agent</label>
              <select
                value={form.agent_id}
                onChange={(e) => setForm({ ...form, agent_id: e.target.value })}
                className="w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary outline-none focus:border-accent/60"
              >
                <option value="">— 选择 Agent —</option>
                {agents.map((a) => (
                  <option key={a.id} value={a.id}>{a.name}（{a.model}）</option>
                ))}
              </select>
            </div>

            <div>
              <label className="text-2xs text-text-muted block mb-1">项目（可选）</label>
              <select
                value={form.project_id}
                onChange={(e) => setForm({ ...form, project_id: e.target.value })}
                className="w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary outline-none focus:border-accent/60"
              >
                <option value="">— 不指定 —</option>
                {projects.map((p) => (
                  <option key={p.id} value={p.id}>{p.name}</option>
                ))}
              </select>
            </div>

            <div>
              <label className="text-2xs text-text-muted block mb-1">触发时的提示词</label>
              <textarea
                value={form.prompt}
                onChange={(e) => setForm({ ...form, prompt: e.target.value })}
                placeholder="例如：查询北京今天天气并简短播报"
                rows={3}
                className="w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent/60 resize-y"
              />
            </div>

            {/* Target channel (optional): lets Agent auto-select target when calling channel.send */}
            <div className="pt-2 border-t border-border/40">
              <p className="text-2xs text-text-muted mb-2">
                目标渠道（可选）— 设置后任务运行时 Agent 可直接调 <code>channel.send</code> 推送到此目标
              </p>
              <div className="space-y-2">
                <select
                  value={form.target_channel_id}
                  onChange={(e) => setForm({ ...form, target_channel_id: e.target.value })}
                  className="w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary outline-none focus:border-accent/60"
                >
                  <option value="">— 不绑定渠道 —</option>
                  {channels.map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.name}（{c.type}）
                    </option>
                  ))}
                </select>
                {form.target_channel_id && (
                  <div className="grid grid-cols-3 gap-2">
                    <select
                      value={form.target_peer_type}
                      onChange={(e) =>
                        setForm({ ...form, target_peer_type: e.target.value as 'user' | 'group' })
                      }
                      className="bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary outline-none focus:border-accent/60"
                    >
                      <option value="user">user（私聊）</option>
                      <option value="group">group（群）</option>
                    </select>
                    <input
                      value={form.target_peer_id}
                      onChange={(e) => setForm({ ...form, target_peer_id: e.target.value })}
                      placeholder="对端 ID（openid 或 group_<gid>）"
                      className="col-span-2 bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent/60 font-mono"
                    />
                  </div>
                )}
              </div>
            </div>

            {formError && <p className="text-xs text-error">{formError}</p>}

            <div className="flex gap-2 pt-1">
              <button
                onClick={submit}
                disabled={saving}
                className="flex-1 py-2 bg-accent text-white text-sm font-medium rounded-lg disabled:opacity-40 hover:bg-accent-hover transition-colors"
              >
                {saving ? '保存中…' : '创建'}
              </button>
              <button
                onClick={() => setShowForm(false)}
                className="flex-1 py-2 bg-surface-tertiary text-text-secondary text-sm rounded-lg hover:text-text-primary transition-colors"
              >
                取消
              </button>
            </div>
          </div>
        </Card>
      )}

      {loading ? (
        <p className="text-sm text-text-muted">加载中…</p>
      ) : jobs.length === 0 && !showForm ? (
        <div className="text-center py-10">
          <p className="text-3xl mb-2">⏰</p>
          <p className="text-sm text-text-muted">暂无定时任务</p>
          <p className="text-2xs text-text-muted mt-1">点击「+ 新建任务」创建第一条</p>
        </div>
      ) : (
        <div className="space-y-2">
          {jobs.map((j) => {
            const agent = agents.find((a) => a.id === j.agent_id)
            const isOpen = expanded === j.id
            const list = runs[j.id] ?? []
            return (
              <Card key={j.id}>
                <div className="px-4 py-3">
                  <div className="flex items-center justify-between gap-3">
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <p className="text-sm text-text-primary truncate">{j.name}</p>
                        <Badge color={j.enabled ? 'green' : 'gray'}>
                          {j.enabled ? '已启用' : '已停用'}
                        </Badge>
                      </div>
                      <div className="flex items-center gap-3 mt-1 flex-wrap">
                        <code className="text-2xs text-text-muted font-mono">{j.spec}</code>
                        {agent && (
                          <span className="text-2xs text-text-muted">→ {agent.name}</span>
                        )}
                        {channelDesc(j) && (
                          <span className="text-2xs text-accent">{channelDesc(j)}</span>
                        )}
                      </div>
                      {j.prompt && (
                        <p className="text-2xs text-text-muted mt-1 truncate" title={j.prompt}>
                          {j.prompt}
                        </p>
                      )}
                    </div>
                    <div className="flex items-center gap-1.5 shrink-0">
                      <Toggle value={j.enabled} onChange={() => toggle(j.id, j.enabled)} />
                      <button
                        onClick={() => runNow(j.id)}
                        disabled={running === j.id}
                        className="text-xs px-2.5 py-1 bg-surface-tertiary text-text-secondary rounded-lg hover:text-accent transition-colors disabled:opacity-40"
                        title="立即执行一次"
                      >
                        {running === j.id ? '…' : '▶'}
                      </button>
                      <button
                        onClick={() => toggleExpand(j.id)}
                        className="text-xs px-2.5 py-1 bg-surface-tertiary text-text-secondary rounded-lg hover:text-text-primary transition-colors"
                      >
                        {isOpen ? '收起' : '历史'}
                      </button>
                      <button
                        onClick={() => remove(j.id)}
                        className={clsx(
                          'text-xs px-2.5 py-1 transition-colors',
                          confirmDel === j.id ? 'text-error' : 'text-text-muted hover:text-error',
                        )}
                      >
                        {confirmDel === j.id ? '确认?' : '删除'}
                      </button>
                    </div>
                  </div>

                  {isOpen && (
                    <div className="mt-3 pt-3 border-t border-border/40">
                      {list.length === 0 ? (
                        <p className="text-2xs text-text-muted">暂无运行记录</p>
                      ) : (
                        <div className="space-y-1">
                          {list.map((r) => (
                            <div key={r.id} className="flex items-center justify-between text-2xs">
                              <span className="text-text-muted font-mono">
                                {fmtTs(r.started_at)}
                              </span>
                              <span
                                className={clsx(
                                  'px-1.5 py-0.5 rounded',
                                  r.status === 'ok'
                                    ? 'bg-success/15 text-success'
                                    : r.status === 'failed'
                                      ? 'bg-error/15 text-error'
                                      : 'bg-warning/15 text-warning',
                                )}
                              >
                                {r.status}
                              </span>
                              {r.error && (
                                <span className="text-error truncate ml-2 flex-1" title={r.error}>
                                  {r.error}
                                </span>
                              )}
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                  )}
                </div>
              </Card>
            )
          })}
        </div>
      )}
    </div>
  )
}
