import { useState, useEffect } from 'react'
import { getWs } from '../../lib/ws'
import { SectionTitle, Card, Badge, Toggle, Row } from './shared-ui'
import type { McpServer } from './types'

export function McpTab() {
  const ws = getWs()
  const [servers, setServers] = useState<McpServer[]>([])
  const [loading, setLoading] = useState(true)
  const [adding, setAdding] = useState(false)
  const [form, setForm] = useState({ name: '', transport: 'stdio', command: '', url: '' })

  const load = () => {
    ws.call<{ servers: McpServer[] }>('mcp.list')
      .then((r) => setServers(r.servers ?? []))
      .catch(() => {})
      .finally(() => setLoading(false))
  }
  useEffect(() => { load() }, [ws])

  const toggle = async (id: string, enabled: boolean) => {
    await ws.call('mcp.update', { id, enabled: !enabled }).catch(() => {})
    load()
  }
  const remove = async (id: string) => {
    await ws.call('mcp.delete', { id }).catch(() => {})
    load()
  }
  const submit = async () => {
    if (!form.name) return
    await ws.call('mcp.add', {
      name: form.name, transport: form.transport,
      command: form.transport === 'stdio' ? form.command : undefined,
      url: form.transport !== 'stdio' ? form.url : undefined,
    }).catch(() => {})
    setAdding(false)
    setForm({ name: '', transport: 'stdio', command: '', url: '' })
    load()
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <SectionTitle>MCP 服务器</SectionTitle>
        <button onClick={() => setAdding(true)}
          className="text-xs px-3 py-1.5 bg-accent/15 text-accent rounded-lg hover:bg-accent/25 transition-colors">
          + 添加
        </button>
      </div>

      {adding && (
        <Card>
          <div className="p-4 space-y-3">
            <p className="text-xs font-medium text-text-primary">添加 MCP 服务器</p>
            <input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="名称"
              className="w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent/60" />
            <select value={form.transport} onChange={(e) => setForm({ ...form, transport: e.target.value })}
              className="w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary outline-none focus:border-accent/60">
              <option value="stdio">stdio</option>
              <option value="sse">SSE</option>
              <option value="http">HTTP</option>
            </select>
            {form.transport === 'stdio'
              ? <input value={form.command} onChange={(e) => setForm({ ...form, command: e.target.value })}
                  placeholder="启动命令，例：npx -y @modelcontextprotocol/server-xxx"
                  className="w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent/60" />
              : <input value={form.url} onChange={(e) => setForm({ ...form, url: e.target.value })}
                  placeholder="URL，例：http://localhost:8080/sse"
                  className="w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent/60" />
            }
            <div className="flex gap-2">
              <button onClick={submit} className="flex-1 py-2 bg-accent text-white text-sm font-medium rounded-lg hover:bg-accent-hover transition-colors">添加</button>
              <button onClick={() => setAdding(false)} className="flex-1 py-2 bg-surface-tertiary text-text-secondary text-sm rounded-lg hover:text-text-primary transition-colors">取消</button>
            </div>
          </div>
        </Card>
      )}

      {loading ? <p className="text-sm text-text-muted">加载中…</p>
        : servers.length === 0 && !adding ? (
          <div className="text-center py-10">
            <p className="text-3xl mb-2">🔌</p>
            <p className="text-sm text-text-muted">暂无 MCP 服务器</p>
          </div>
        ) : (
          <Card>
            {servers.map((s) => (
              <div key={s.id} className="flex items-center justify-between px-4 py-3 border-b border-border/40 last:border-0">
                <div className="min-w-0">
                  <p className="text-sm text-text-primary truncate">{s.name}</p>
                  <p className="text-2xs text-text-muted truncate mt-0.5">{s.command || s.url || '—'}</p>
                </div>
                <div className="flex items-center gap-2 ml-3 shrink-0">
                  <Badge color={s.transport === 'stdio' ? 'orange' : 'green'}>{s.transport}</Badge>
                  <Toggle value={s.enabled} onChange={() => toggle(s.id, s.enabled)} />
                  <button onClick={() => remove(s.id)} className="text-text-muted hover:text-error transition-colors p-1">
                    <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
                      <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
                    </svg>
                  </button>
                </div>
              </div>
            ))}
          </Card>
        )}
    </div>
  )
}
