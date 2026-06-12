import { useState } from 'react'
import { clsx } from 'clsx'
import { useAgents } from '../../hooks/use-agents'
import { AgentFormDialog } from '../agents/AgentFormDialog'
import { getWs } from '../../lib/ws'
import { SectionTitle, Card } from './shared-ui'
import type { Agent } from '../../types'

export function AgentsTab() {
  const { agents, loading, load } = useAgents()

  const [showForm, setShowForm] = useState(false)
  const [editTarget, setEditTarget] = useState<Agent | null>(null)
  const [deleting, setDeleting] = useState<string | null>(null)

  const handleEdit = (a: Agent) => { setEditTarget(a); setShowForm(true) }
  const handleNew = () => { setEditTarget(null); setShowForm(true) }
  const handleSave = (a: Agent) => { void a; load(); setShowForm(false) }
  const handleDelete = async (id: string) => {
    setDeleting(id)
    try { await getWs().call('agents.delete', { id }); load() } catch { /* ignore */ }
    setDeleting(null)
  }

  return (
    <>
      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <SectionTitle>Agent 管理</SectionTitle>
          <button onClick={handleNew}
            className="text-xs px-3 py-1.5 bg-accent/15 text-accent rounded-lg hover:bg-accent/25 transition-colors">
            + 新建 Agent
          </button>
        </div>

        {loading ? (
          <p className="text-sm text-text-muted">加载中…</p>
        ) : agents.length === 0 ? (
          <div className="text-center py-10">
            <p className="text-3xl mb-2">🤖</p>
            <p className="text-sm text-text-muted">暂无 Agent，点击「新建 Agent」创建第一个</p>
          </div>
        ) : (
          <div className="space-y-2">
            {agents.map((a) => {
              const ae = a as Agent & { emoji?: string }
              return (
                <Card key={a.id}>
                  <div className="flex items-center justify-between px-4 py-3">
                    <div className="flex items-center gap-3 min-w-0">
                      <div className="w-9 h-9 rounded-xl bg-surface-tertiary flex items-center justify-center text-lg shrink-0">
                        {ae.emoji ?? '🤖'}
                      </div>
                      <div className="min-w-0">
                        <p className="text-sm font-medium text-text-primary">{a.name}</p>
                        <p className="text-2xs text-text-muted font-mono truncate">{a.model}</p>
                        {a.system_prompt && (
                          <p className="text-2xs text-text-muted truncate max-w-xs mt-0.5">{a.system_prompt}</p>
                        )}
                      </div>
                    </div>
                    <div className="flex items-center gap-2 ml-3 shrink-0">
                      <span className={clsx('text-2xs px-1.5 py-0.5 rounded-md',
                        a.enabled ? 'bg-success/15 text-success' : 'bg-surface-tertiary text-text-muted')}>
                        {a.enabled ? '启用' : '停用'}
                      </span>
                      <button onClick={() => handleEdit(a)}
                        className="text-xs px-2.5 py-1 bg-surface-tertiary text-text-secondary rounded-lg hover:text-text-primary transition-colors">
                        编辑
                      </button>
                      <button onClick={() => handleDelete(a.id)}
                        disabled={deleting === a.id}
                        className="text-xs px-2.5 py-1 text-text-muted hover:text-error transition-colors disabled:opacity-40">
                        {deleting === a.id ? '…' : '删除'}
                      </button>
                    </div>
                  </div>
                </Card>
              )
            })}
          </div>
        )}
      </div>

      {showForm && (
        <AgentFormDialog
          agent={editTarget}
          onSave={handleSave}
          onCancel={() => setShowForm(false)}
        />
      )}
    </>
  )
}
