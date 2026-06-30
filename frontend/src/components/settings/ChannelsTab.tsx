import { useState, useEffect } from 'react'
import { clsx } from 'clsx'
import { getWs } from '../../lib/ws'
import { useAgents } from '../../hooks/use-agents'
import { SectionTitle, Card, Badge, Toggle } from './shared-ui'
import type { Channel, ChannelType, QQConfig, WeWorkConfig, DingTalkConfig } from './types'

interface ChannelFormState {
  type: ChannelType
  name: string
  agent_id: string
  // QQ
  app_id: string
  app_secret: string
  sandbox: boolean
  // WeWork
  corp_id: string
  corp_secret: string
  wework_agent_id: string
  wework_callback_port: string
  // DingTalk
  webhook_url: string
  ding_secret: string
  ding_app_key: string
  ding_app_secret: string
  robot_code: string
  ding_callback_port: string
}

const emptyChannelForm: ChannelFormState = {
  type: 'qq',
  name: '',
  agent_id: '',
  app_id: '',
  app_secret: '',
  sandbox: false,
  corp_id: '',
  corp_secret: '',
  wework_agent_id: '',
  wework_callback_port: '',
  webhook_url: '',
  ding_secret: '',
  ding_app_key: '',
  ding_app_secret: '',
  robot_code: '',
  ding_callback_port: '',
}

const typeLabel = (t: string) => {
  switch (t) {
    case 'qq': return 'QQ 官方机器人'
    case 'wechat': return '微信'
    case 'wework': return '企业微信'
    case 'dingtalk': return '钉钉'
    default: return t
  }
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
    const cfg = (c.config ?? {}) as Record<string, any>
    setForm({
      ...emptyChannelForm,
      type: (c.type as ChannelType) || 'qq',
      name: c.name,
      agent_id: c.agent_id ?? '',
      app_id: cfg.app_id ?? '',
      app_secret: cfg.app_secret ?? '',
      sandbox: !!cfg.sandbox,
      corp_id: cfg.corp_id ?? '',
      corp_secret: cfg.corp_secret ?? '',
      wework_agent_id: cfg.agent_id ? String(cfg.agent_id) : '',
      wework_callback_port: cfg.callback_port ? String(cfg.callback_port) : '',
      webhook_url: cfg.webhook_url ?? '',
      ding_secret: cfg.secret ?? '',
      ding_app_key: cfg.app_key ?? '',
      ding_app_secret: cfg.app_secret ?? '',
      robot_code: cfg.robot_code ?? '',
      ding_callback_port: cfg.callback_port ? String(cfg.callback_port) : '',
    })
    setFormError(null)
    setShowForm(true)
  }

  const buildConfig = (): Record<string, any> | null => {
    if (form.type === 'qq') {
      if (!form.app_id.trim() || !form.app_secret.trim()) {
        setFormError('QQ 渠道需要填写 AppID 与 AppSecret')
        return null
      }
      return {
        app_id: form.app_id.trim(),
        app_secret: form.app_secret.trim(),
        sandbox: form.sandbox,
      }
    }
    if (form.type === 'wework') {
      if (!form.corp_id.trim() || !form.corp_secret.trim() || !form.wework_agent_id.trim()) {
        setFormError('企业微信需要填写 CorpID / CorpSecret / AgentID')
        return null
      }
      const cfg: WeWorkConfig = {
        corp_id: form.corp_id.trim(),
        corp_secret: form.corp_secret.trim(),
        agent_id: Number(form.wework_agent_id),
      }
      if (form.wework_callback_port.trim()) cfg.callback_port = Number(form.wework_callback_port)
      return cfg as any
    }
    if (form.type === 'dingtalk') {
      const hasWebhook = !!form.webhook_url.trim()
      const hasApp = !!form.ding_app_key.trim() && !!form.ding_app_secret.trim()
      if (!hasWebhook && !hasApp) {
        setFormError('钉钉需要填写自定义机器人 Webhook，或企业内部应用 AppKey + AppSecret')
        return null
      }
      const cfg: DingTalkConfig = {}
      if (hasWebhook) {
        cfg.webhook_url = form.webhook_url.trim()
        if (form.ding_secret.trim()) cfg.secret = form.ding_secret.trim()
      }
      if (hasApp) {
        cfg.app_key = form.ding_app_key.trim()
        cfg.app_secret = form.ding_app_secret.trim()
        if (form.robot_code.trim()) cfg.robot_code = form.robot_code.trim()
      }
      if (form.ding_callback_port.trim()) cfg.callback_port = Number(form.ding_callback_port)
      return cfg as any
    }
    if (form.type === 'wechat') {
      setFormError('微信渠道暂未提供完整实现，请使用其它渠道')
      return null
    }
    return {}
  }

  const submit = async () => {
    if (!form.name.trim()) {
      setFormError('请填写渠道名称')
      return
    }
    const config = buildConfig()
    if (!config) return

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

  const renderTypeFields = () => {
    if (form.type === 'qq') {
      return (
        <>
          <Field label="AppID">
            <input value={form.app_id} onChange={(e) => setForm({ ...form, app_id: e.target.value })}
              placeholder="QQ 开放平台 → 机器人 → AppID" className={inputCls} />
          </Field>
          <Field label="AppSecret">
            <input value={form.app_secret} type="password"
              onChange={(e) => setForm({ ...form, app_secret: e.target.value })}
              placeholder="QQ 开放平台 → 机器人 → AppSecret" className={inputCls} />
          </Field>
          <div className="flex items-center justify-between pt-1">
            <div className="min-w-0 mr-3">
              <p className="text-sm text-text-primary">沙箱环境</p>
              <p className="text-2xs text-text-muted mt-0.5">开启后走 QQ Bot 沙箱网关，仅自测使用</p>
            </div>
            <Toggle value={form.sandbox} onChange={(v) => setForm({ ...form, sandbox: v })} />
          </div>
        </>
      )
    }
    if (form.type === 'wework') {
      return (
        <>
          <Field label="CorpID">
            <input value={form.corp_id} onChange={(e) => setForm({ ...form, corp_id: e.target.value })}
              placeholder="企业微信管理后台 → 我的企业 → 企业 ID" className={inputCls} />
          </Field>
          <Field label="CorpSecret">
            <input value={form.corp_secret} type="password"
              onChange={(e) => setForm({ ...form, corp_secret: e.target.value })}
              placeholder="自建应用 → Secret" className={inputCls} />
          </Field>
          <Field label="AgentID（应用 ID）">
            <input value={form.wework_agent_id}
              onChange={(e) => setForm({ ...form, wework_agent_id: e.target.value })}
              placeholder="应用详情中的 AgentId（数字）" className={inputCls} />
          </Field>
          <Field label="回调端口（可选）">
            <input value={form.wework_callback_port}
              onChange={(e) => setForm({ ...form, wework_callback_port: e.target.value })}
              placeholder="默认 7787" className={inputCls} />
          </Field>
          <p className="text-2xs text-text-muted leading-relaxed">
            💡 入站消息通过 <code>/wework/callback</code> 接收 JSON。生产环境建议在前面挂 XML 解密代理。
          </p>
        </>
      )
    }
    if (form.type === 'dingtalk') {
      return (
        <>
          <p className="text-2xs text-text-muted">两种发送方式任选其一（或同时配置，优先使用 Webhook）：</p>
          <Field label="自定义群机器人 Webhook URL（简单模式）">
            <input value={form.webhook_url}
              onChange={(e) => setForm({ ...form, webhook_url: e.target.value })}
              placeholder="https://oapi.dingtalk.com/robot/send?access_token=..." className={inputCls} />
          </Field>
          <Field label="加签 Secret（可选）">
            <input value={form.ding_secret}
              onChange={(e) => setForm({ ...form, ding_secret: e.target.value })}
              placeholder="自定义机器人加签密钥" className={inputCls} />
          </Field>
          <div className="border-t border-border/40 pt-3" />
          <Field label="AppKey（企业内部应用模式）">
            <input value={form.ding_app_key}
              onChange={(e) => setForm({ ...form, ding_app_key: e.target.value })}
              placeholder="开发者后台 → 应用 → AppKey" className={inputCls} />
          </Field>
          <Field label="AppSecret">
            <input value={form.ding_app_secret} type="password"
              onChange={(e) => setForm({ ...form, ding_app_secret: e.target.value })}
              placeholder="开发者后台 → 应用 → AppSecret" className={inputCls} />
          </Field>
          <Field label="RobotCode">
            <input value={form.robot_code}
              onChange={(e) => setForm({ ...form, robot_code: e.target.value })}
              placeholder="企业内部应用机器人 RobotCode" className={inputCls} />
          </Field>
          <Field label="回调端口（可选）">
            <input value={form.ding_callback_port}
              onChange={(e) => setForm({ ...form, ding_callback_port: e.target.value })}
              placeholder="默认 7788" className={inputCls} />
          </Field>
        </>
      )
    }
    if (form.type === 'wechat') {
      return (
        <p className="text-2xs text-text-muted">
          微信通道当前仅注册占位骨架，建议接入第三方 Gewechat 等网关后再启用。
        </p>
      )
    }
    return null
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <SectionTitle>渠道列表</SectionTitle>
        <button onClick={openNew}
          className="text-xs px-3 py-1.5 bg-accent/15 text-accent rounded-lg hover:bg-accent/25 transition-colors">
          + 新建渠道
        </button>
      </div>

      {showForm && (
        <Card>
          <div className="p-4 space-y-3">
            <p className="text-xs font-medium text-text-primary">
              {editTarget ? '编辑渠道' : '新建渠道'}
            </p>

            <Field label="类型">
              <select value={form.type} disabled={!!editTarget}
                onChange={(e) => setForm({ ...form, type: e.target.value as ChannelType })}
                className={inputCls + ' disabled:opacity-60'}>
                <option value="qq">QQ 官方机器人</option>
                <option value="wework">企业微信</option>
                <option value="dingtalk">钉钉</option>
                <option value="wechat" disabled>微信（暂未支持）</option>
              </select>
            </Field>

            <Field label="名称">
              <input value={form.name} autoFocus
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                placeholder="例如：我的机器人" className={inputCls} />
            </Field>

            <Field label="默认 Agent（消息无路由时回退到该 Agent）">
              <select value={form.agent_id}
                onChange={(e) => setForm({ ...form, agent_id: e.target.value })}
                className={inputCls}>
                <option value="">— 不绑定 —</option>
                {agents.map((a) => (
                  <option key={a.id} value={a.id}>{a.name}（{a.model}）</option>
                ))}
              </select>
            </Field>

            {renderTypeFields()}

            <p className="text-2xs text-text-muted leading-relaxed">
              💡 配置生效需重启应用：当前进程在启动时加载已启用渠道并建立连接。
            </p>

            {formError && <p className="text-xs text-error">{formError}</p>}

            <div className="flex gap-2 pt-1">
              <button onClick={submit} disabled={saving}
                className="flex-1 py-2 bg-accent text-white text-sm font-medium rounded-lg disabled:opacity-40 hover:bg-accent-hover transition-colors">
                {saving ? '保存中…' : editTarget ? '保存' : '创建'}
              </button>
              <button onClick={() => setShowForm(false)}
                className="flex-1 py-2 bg-surface-tertiary text-text-secondary text-sm rounded-lg hover:text-text-primary transition-colors">
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
          <p className="text-2xs text-text-muted mt-1">点击右上角「+ 新建渠道」添加机器人</p>
        </div>
      ) : (
        <Card>
          {channels.map((c) => {
            const cfg = (c.config ?? {}) as Record<string, any>
            let desc = typeLabel(c.type)
            if (c.type === 'qq') {
              const q = cfg as QQConfig
              desc = `QQ · AppID ${q.app_id ? q.app_id.slice(0, 6) + '…' : '未配置'}${q.sandbox ? ' · 沙箱' : ''}`
            } else if (c.type === 'wework') {
              desc = `企业微信 · CorpID ${cfg.corp_id ? String(cfg.corp_id).slice(0, 8) + '…' : '未配置'} · Agent ${cfg.agent_id ?? '?'}`
            } else if (c.type === 'dingtalk') {
              desc = cfg.webhook_url ? '钉钉 · 自定义机器人' : `钉钉 · 企业应用 ${cfg.app_key ? String(cfg.app_key).slice(0, 6) + '…' : ''}`
            }
            return (
              <div key={c.id}
                className="flex items-center justify-between px-4 py-3 border-b border-border/40 last:border-0">
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
                  <button onClick={() => openEdit(c)}
                    className="text-xs px-2.5 py-1 bg-surface-tertiary text-text-secondary rounded-lg hover:text-text-primary transition-colors">
                    编辑
                  </button>
                  <button onClick={() => remove(c.id)} disabled={deleting === c.id}
                    className={clsx(
                      'text-xs px-2.5 py-1 transition-colors disabled:opacity-40',
                      confirmDel === c.id ? 'text-error' : 'text-text-muted hover:text-error',
                    )}>
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

const inputCls =
  'w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent/60'

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <label className="text-2xs text-text-muted block mb-1">{label}</label>
      {children}
    </div>
  )
}
