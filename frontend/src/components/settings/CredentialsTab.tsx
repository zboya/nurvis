import { useState, useEffect } from 'react'
import { clsx } from 'clsx'
import { getWs } from '../../lib/ws'
import { SectionTitle, Card, Badge } from './shared-ui'
import type { Credential } from './types'

interface CredentialFormState {
  provider: string
  name: string
  api_token: string
  account_id: string
}

const emptyCredForm: CredentialFormState = {
  provider: 'cloudflare',
  name: '',
  api_token: '',
  account_id: '',
}

export function CredentialsTab() {
  const ws = getWs()
  const [credentials, setCredentials] = useState<Credential[]>([])
  const [loading, setLoading] = useState(true)
  const [showForm, setShowForm] = useState(false)
  const [editTarget, setEditTarget] = useState<Credential | null>(null)
  const [form, setForm] = useState<CredentialFormState>(emptyCredForm)
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<string | null>(null)
  const [confirmDel, setConfirmDel] = useState<string | null>(null)

  const load = () => {
    setLoading(true)
    ws.call<{ credentials: Credential[] }>('credentials.list')
      .then((r) => setCredentials(r.credentials ?? []))
      .catch(() => {})
      .finally(() => setLoading(false))
  }
  useEffect(() => { load() }, [ws])

  const openNew = () => {
    setEditTarget(null)
    setForm(emptyCredForm)
    setFormError(null)
    setShowForm(true)
  }

  const openEdit = (c: Credential) => {
    setEditTarget(c)
    setForm({
      provider: c.provider,
      name: c.name,
      api_token: '',
      account_id: '',
    })
    setFormError(null)
    setShowForm(true)
  }

  const submit = async () => {
    if (!form.name.trim()) { setFormError('请填写名称'); return }
    if (!form.api_token.trim() && !editTarget) { setFormError('请填写 API Token'); return }
    if (!form.account_id.trim() && !editTarget) { setFormError('请填写 Account ID'); return }

    const config: Record<string, string> = {}
    if (form.api_token.trim()) config.api_token = form.api_token.trim()
    if (form.account_id.trim()) config.account_id = form.account_id.trim()

    setSaving(true)
    setFormError(null)
    try {
      if (editTarget) {
        const params: Record<string, any> = { id: editTarget.id, name: form.name.trim() }
        if (Object.keys(config).length > 0) params.config = config
        await ws.call('credentials.update', params)
      } else {
        await ws.call('credentials.create', {
          name: form.name.trim(),
          provider: form.provider,
          config,
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

  const remove = async (id: string) => {
    if (confirmDel !== id) {
      setConfirmDel(id)
      setTimeout(() => setConfirmDel((cur) => (cur === id ? null : cur)), 3000)
      return
    }
    setConfirmDel(null)
    setDeleting(id)
    try {
      await ws.call('credentials.delete', { id })
      load()
    } catch { /* ignore */ }
    setDeleting(null)
  }

  const providerLabel = (p: string) =>
    p === 'cloudflare' ? 'Cloudflare Pages' : p

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <SectionTitle>网站凭证</SectionTitle>
        <button onClick={openNew}
          className="text-xs px-3 py-1.5 bg-accent/15 text-accent rounded-lg hover:bg-accent/25 transition-colors">
          + 添加凭证
        </button>
      </div>

      <p className="text-2xs text-text-muted">
        配置网站部署平台的凭证，供 Agent 发布静态网站时使用（如 Cloudflare Pages）。
      </p>

      {showForm && (
        <Card>
          <div className="p-4 space-y-3">
            <p className="text-xs font-medium text-text-primary">
              {editTarget ? '编辑凭证' : '添加凭证'}
            </p>

            <div>
              <label className="text-2xs text-text-muted block mb-1">平台</label>
              <select
                value={form.provider}
                disabled={!!editTarget}
                onChange={(e) => setForm({ ...form, provider: e.target.value })}
                className="w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary outline-none focus:border-accent/60 disabled:opacity-60"
              >
                <option value="cloudflare">Cloudflare Pages</option>
              </select>
            </div>

            <div>
              <label className="text-2xs text-text-muted block mb-1">名称</label>
              <input
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                placeholder="例如：我的 CF 账号"
                autoFocus
                className="w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent/60"
              />
            </div>

            {form.provider === 'cloudflare' && (
              <>
                <div>
                  <label className="text-2xs text-text-muted block mb-1">Account ID</label>
                  <input
                    value={form.account_id}
                    onChange={(e) => setForm({ ...form, account_id: e.target.value })}
                    placeholder="Cloudflare Dashboard → 概述 → Account ID"
                    className="w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent/60 font-mono"
                  />
                </div>
                <div>
                  <label className="text-2xs text-text-muted block mb-1">API Token</label>
                  <input
                    value={form.api_token}
                    onChange={(e) => setForm({ ...form, api_token: e.target.value })}
                    placeholder={editTarget ? '留空保持不变' : 'Cloudflare → My Profile → API Tokens'}
                    type="password"
                    className="w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent/60 font-mono"
                  />
                </div>
                <p className="text-2xs text-text-muted leading-relaxed">
                  💡 API Token 需要具有 Cloudflare Pages 的编辑权限。创建 Token 时选择 "Edit Cloudflare Pages" 模板。
                </p>
              </>
            )}

            {formError && <p className="text-xs text-error">{formError}</p>}

            <div className="flex gap-2 pt-1">
              <button onClick={submit} disabled={saving}
                className="flex-1 py-2 bg-accent text-white text-sm font-medium rounded-lg disabled:opacity-40 hover:bg-accent-hover transition-colors">
                {saving ? '保存中…' : editTarget ? '保存' : '添加'}
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
      ) : credentials.length === 0 && !showForm ? (
        <div className="text-center py-10">
          <p className="text-3xl mb-2">🔑</p>
          <p className="text-sm text-text-muted">暂无凭证配置</p>
          <p className="text-2xs text-text-muted mt-1">添加 Cloudflare 凭证后，Agent 可使用 publish_cloudflare_pages 工具发布网站</p>
        </div>
      ) : (
        <Card>
          {credentials.map((c) => (
            <div key={c.id} className="flex items-center justify-between px-4 py-3 border-b border-border/40 last:border-0">
              <div className="min-w-0 mr-3">
                <div className="flex items-center gap-2">
                  <p className="text-sm text-text-primary truncate">{c.name}</p>
                  <Badge color={c.enabled ? 'green' : 'gray'}>
                    {c.enabled ? '已启用' : '已停用'}
                  </Badge>
                </div>
                <p className="text-2xs text-text-muted mt-0.5">{providerLabel(c.provider)}</p>
              </div>
              <div className="flex items-center gap-2 shrink-0">
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
          ))}
        </Card>
      )}
    </div>
  )
}
