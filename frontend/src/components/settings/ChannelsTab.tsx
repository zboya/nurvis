import { useState, useEffect } from 'react'
import { clsx } from 'clsx'
import { getWs } from '../../lib/ws'
import { useAgents } from '../../hooks/use-agents'
import { SectionTitle, Card, Badge, Toggle } from './shared-ui'
import type { Channel, ChannelType, QQConfig } from './types'

interface ChannelFormState {
  type: ChannelType
  name: string
  agent_id: string
  // QQ
  app_id: string
  app_secret: string
  sandbox: boolean
}

const emptyChannelForm: ChannelFormState = {
  type: 'qq',
  name: '',
  agent_id: '',
  app_id: '',
  app_secret: '',
  sandbox: false,
}

export function ChannelsTab() {
  const ws = getWs()
  const { agents } = useAgents()
  const [channels, setChannels] = useState<Channel[]>([])
  const [loading, setLoading] = useState(true)

  const [showForm, setShowForm] = useState(false)
  const [editTarget, setEditTarget] = useState<Channel | null>(null)
  const [form, setForm] = useState<ChannelFormState>(emptyChannelForm)
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<string | null>(null)
  const [confirmDel, setConfirmDel] = useState<string | null>(null)

  const load = () => {
    setLoading(true)
    ws.call<{ channels: Channel[] }>('channels.list')
      .then((r) => setChannels(r.channels ?? []))
      .catch(() => {})
      .finally(() => setLoading(false))
  }
  useEffect(() => { load() }, [ws])

  const openNew = () => {
    setEditTarget(null)
    setForm(emptyChannelForm)
    setFormError(null)
    setShowForm(true)
  }

  const openEdit = (c: Channel) => {
    setEditTarget(c)
    const cfg = (c.config ?? {}) as QQConfig
    setForm({
      type: (c.type as ChannelType) || 'qq',
      name: c.name,
      agent_id: c.agent_id ?? '',
      app_id: cfg.app_id ?? '',
      app_secret: cfg.app_secret ?? '',
      sandbox: !!cfg.sandbox,
    })
    setFormError(null)
    setShowForm(true)
  }

  const submit = async () => {
    if (!form.name.trim()) {
      setFormError('请填写渠道名称')
      return
    }
    if (form.type === 'qq') {
      if (!form.app_id.trim() || !form.app_secret.trim()) {
        setFormError('QQ 渠道需要填写 AppID 与 AppSecret')
        return
      }
    }

    let config: Record<string, any> = {}
    if (form.type === 'qq') {
      config = {
        app_id: form.app_id.trim(),
        app_secret: form.app_secret.trim(),
        sandbox: form.sandbox,
      }
    }

    setSaving(true)
    setFormError(null)
    try {
      if (editTarget) {
        await ws.call('channels.update', {
          id: editTarget.id,
          name: form.name.trim(),
          config,
          agent_id: form.agent_id || '',
        })
      } else {
        await ws.call('channels.create', {
          type: form.type,
          name: form.name.trim(),
          config,
          agent_id: form.agent_id || '',
        })
      }
      setShowForm(false)
      load()
    } catch (e: any) {
      setFormError(e?.message ?? '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const toggle = async (id: string, enabled: boolean) => {
    await ws.call('channels.update', { id, enabled: !enabled }).catch(() => {})
    load()
  }

  const remove = async (id: string) => {
    if (confirmDel !== id) {
      setConfirmDel(id)
      setTimeout(() => setConfirmDel((cur) => (cur === id ? null : cur)), 3000)
      return
    }
    setConfirmDel(null)
    setDeleting(id)
    try {
      await ws.call('channels.delete', { id })
      load()
    } catch { /* ignore */ }
    setDeleting(null)
  }

  const typeLabel = (t: string) =>
    t === 'qq' ? 'QQ 官方机器人' : t === 'wechat' ? '微信' : t

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <SectionTitle>渠道列表</SectionTitle>
        <button
          onClick={openNew}
          className="text-xs px-3 py-1.5 bg-accent/15 text-accent rounded-lg hover:bg-accent/25 transition-colors"
        >
          + 新建渠道
        </button>
      </div>

      {showForm && (
        <Card>
          <div className="p-4 space-y-3">
            <p className="text-xs font-medium text-text-primary">
              {editTarget ? '编辑渠道' : '新建渠道'}
            </p>

            {/* Type selection (type change disabled in edit mode) */}
            <div>
              <label className="text-2xs text-text-muted block mb-1">类型</label>
              <select
                value={form.type}
                disabled={!!editTarget}
                onChange={(e) => setForm({ ...form, type: e.target.value as ChannelType })}
                className="w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary outline-none focus:border-accent/60 disabled:opacity-60"
              >
                <option value="qq">QQ 官方机器人</option>
                <option value="wechat" disabled>微信（暂未支持）</option>
              </select>
            </div>

            {/* Name */}
            <div>
              <label className="text-2xs text-text-muted block mb-1">名称</label>
              <input
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                placeholder="例如：我的 QQ 机器人"
                autoFocus
                className="w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent/60"
              />
            </div>

            {/* Default Agent (optional) */}
            <div>
              <label className="text-2xs text-text-muted block mb-1">默认 Agent（消息无路由时回退到该 Agent）</label>
              <select
                value={form.agent_id}
                onChange={(e) => setForm({ ...form, agent_id: e.target.value })}
                className="w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary outline-none focus:border-accent/60"
              >
                <option value="">— 不绑定 —</option>
                {agents.map((a) => (
                  <option key={a.id} value={a.id}>{a.name}（{a.model}）</option>
                ))}
              </select>
            </div>

            {/* QQ-specific fields */}
            {form.type === 'qq' && (
              <>
                <div>
                  <label className="text-2xs text-text-muted block mb-1">AppID</label>
                  <input
                    value={form.app_id}
                    onChange={(e) => setForm({ ...form, app_id: e.target.value })}
                    placeholder="QQ 开放平台 → 机器人 → AppID"
                    className="w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent/60 font-mono"
                  />
                </div>
                <div>
                  <label className="text-2xs text-text-muted block mb-1">AppSecret</label>
                  <input
                    value={form.app_secret}
                    onChange={(e) => setForm({ ...form, app_secret: e.target.value })}
                    placeholder="QQ 开放平台 → 机器人 → AppSecret"
                    type="password"
                    className="w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent/60 font-mono"
                  />
                </div>
                <div className="flex items-center justify-between pt-1">
                  <div className="min-w-0 mr-3">
                    <p className="text-sm text-text-primary">沙箱环境</p>
                    <p className="text-2xs text-text-muted mt-0.5">开启后走 QQ Bot 沙箱网关，仅自测使用</p>
                  </div>
                  <Toggle
                    value={form.sandbox}
                    onChange={(v) => setForm({ ...form, sandbox: v })}
                  />
                </div>
                <p className="text-2xs text-text-muted leading-relaxed">
                  💡 配置生效需重启应用：当前进程在启动时加载已启用渠道并建立 WebSocket 长连接。
                </p>
              </>
            )}

            {formError && <p className="text-xs text-error">{formError}</p>}

            <div className="flex gap-2 pt-1">
              <button
                onClick={submit}
                disabled={saving}
                className="flex-1 py-2 bg-accent text-white text-sm font-medium rounded-lg disabled:opacity-40 hover:bg-accent-hover transition-colors"
              >
                {saving ? '保存中…' : editTarget ? '保存' : '创建'}
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
      ) : channels.length === 0 && !showForm ? (
        <div className="text-center py-10">
          <p className="text-3xl mb-2">📡</p>
          <p className="text-sm text-text-muted">暂无渠道配置</p>
          <p className="text-2xs text-text-muted mt-1">点击右上角「+ 新建渠道」添加 QQ 机器人</p>
        </div>
      ) : (
        <Card>
          {channels.map((c) => {
            const cfg = (c.config ?? {}) as QQConfig
            const desc =
              c.type === 'qq'
                ? `QQ · AppID ${cfg.app_id ? cfg.app_id.slice(0, 6) + '…' : '未配置'}${cfg.sandbox ? ' · 沙箱' : ''}`
                : typeLabel(c.type)
            return (
              <div
                key={c.id}
                className="flex items-center justify-between px-4 py-3 border-b border-border/40 last:border-0"
              >
                <div className="min-w-0 mr-3">
                  <div className="flex items-center gap-2">
                    <p className="text-sm text-text-primary truncate">{c.name}</p>
                    <Badge color={c.enabled ? 'green' : 'gray'}>
                      {c.enabled ? '已启用' : '已停用'}
                    </Badge>
                  </div>
                  <p className="text-2xs text-text-muted mt-0.5 truncate">{desc}</p>
                </div>
                <div className="flex items-center gap-2 shrink-0">
                  <Toggle value={c.enabled} onChange={() => toggle(c.id, c.enabled)} />
                  <button
                    onClick={() => openEdit(c)}
                    className="text-xs px-2.5 py-1 bg-surface-tertiary text-text-secondary rounded-lg hover:text-text-primary transition-colors"
                  >
                    编辑
                  </button>
                  <button
                    onClick={() => remove(c.id)}
                    disabled={deleting === c.id}
                    className={clsx(
                      'text-xs px-2.5 py-1 transition-colors disabled:opacity-40',
                      confirmDel === c.id ? 'text-error' : 'text-text-muted hover:text-error',
                    )}
                  >
                    {deleting === c.id ? '…' : confirmDel === c.id ? '确认?' : '删除'}
                  </button>
                </div>
              </div>
            )
          })}
        </Card>
      )}
    </div>
  )
}
