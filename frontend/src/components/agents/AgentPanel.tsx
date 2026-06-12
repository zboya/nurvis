import { useState } from 'react'
import type { Agent } from '../../types'
import { useAgents } from '../../hooks/use-agents'
import { useUiStore } from '../../stores/ui-store'
import { AgentFormDialog } from './AgentFormDialog'
import { AgentTagBadge } from './AgentTagBadge'
import { Button, Spinner } from '../ui'

export function AgentPanel() {
  const { agents, loading, load, remove } = useAgents()
  const activeAgentId = useUiStore((s) => s.activeAgentId)
  const setActiveAgent = useUiStore((s) => s.setActiveAgent)
  const setView = useUiStore((s) => s.setView)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editAgent, setEditAgent] = useState<Agent | null>(null)

  const handleSave = async (_agent: Agent) => {
    await load()
    setDialogOpen(false)
    setEditAgent(null)
  }

  const handleSelect = (agent: Agent) => {
    setActiveAgent(agent.id)
    setView('chat')
  }

  return (
    <div className="h-full flex flex-col">
      {/* Header */}
      <div className="flex items-center justify-between px-4 py-3 border-b border-border shrink-0">
        <h2 className="text-sm font-semibold text-text-primary">助手管理</h2>
        <Button
          variant="primary"
          size="xs"
          onClick={() => { setEditAgent(null); setDialogOpen(true) }}
        >
          <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth={2.5} viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" d="M12 4v16m8-8H4" />
          </svg>
          新建助手
        </Button>
      </div>

      {/* Agent list */}
      <div className="flex-1 overflow-y-auto p-3 space-y-2">
        {loading && (
          <div className="flex items-center justify-center py-10">
            <Spinner />
          </div>
        )}
        {!loading && agents.length === 0 && (
          <div className="text-center py-12">
            <div className="text-4xl mb-3">🤖</div>
            <p className="text-sm text-text-muted">还没有助手</p>
            <p className="text-xs text-text-muted mt-1">点击右上角创建你的第一个助手</p>
          </div>
        )}
        {agents.map((agent) => {
          const a = agent as Agent & { emoji?: string }
          return (
            <div
              key={agent.id}
              className={[
                'group flex items-center gap-3 p-3 rounded-xl border transition-all cursor-pointer',
                activeAgentId === agent.id
                  ? 'border-accent/50 bg-accent/5'
                  : 'border-border hover:border-border hover:bg-surface-tertiary/40',
              ].join(' ')}
              onClick={() => handleSelect(agent)}
            >
              {/* Avatar */}
              <div className="w-10 h-10 rounded-xl bg-surface-tertiary border border-border flex items-center justify-center text-xl shrink-0">
                {a.emoji ?? '🤖'}
              </div>

              {/* Info */}
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-1.5">
                  <p className="text-sm font-medium text-text-primary truncate">{agent.name}</p>
                  <AgentTagBadge tag={agent.tag} size="xs" />
                </div>
                <p className="text-xs text-text-muted truncate mt-0.5">{agent.model}</p>
              </div>

              {/* Enabled badge */}
              <div className={[
                'w-1.5 h-1.5 rounded-full shrink-0',
                agent.enabled ? 'bg-success' : 'bg-text-muted',
              ].join(' ')} />

              {/* Actions */}
              <div className="flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity" onClick={(e) => e.stopPropagation()}>
                <button
                  onClick={() => { setEditAgent(agent); setDialogOpen(true) }}
                  className="p-1.5 rounded-lg text-text-muted hover:text-text-primary hover:bg-surface-tertiary transition-colors"
                  title="编辑"
                >
                  <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
                  </svg>
                </button>
                <button
                  onClick={async () => {
                    if (confirm(`Confirm delete "${agent.name}"?`)) {
                      await remove(agent.id)
                      if (activeAgentId === agent.id) setActiveAgent(null)
                    }
                  }}
                  className="p-1.5 rounded-lg text-text-muted hover:text-error hover:bg-error/10 transition-colors"
                  title="删除"
                >
                  <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                  </svg>
                </button>
              </div>
            </div>
          )
        })}
      </div>

      {dialogOpen && (
        <AgentFormDialog
          agent={editAgent}
          onSave={(a) => { handleSave(a) }}
          onCancel={() => { setDialogOpen(false); setEditAgent(null) }}
        />
      )}
    </div>
  )
}
