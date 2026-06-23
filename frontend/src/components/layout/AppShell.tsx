import { type ReactNode } from 'react'
import { Sidebar } from './Sidebar'
import { useUiStore } from '../../stores/ui-store'
import { ChatCanvas } from '../chat/ChatCanvas'
import { SettingsPanel } from '../settings/SettingsPanel'
import { Spinner } from '../ui'
import { clsx } from 'clsx'

export function AppShell() {
  const view = useUiStore((s) => s.view)
  const sidebarOpen = useUiStore((s) => s.sidebarOpen)
  const pendingAgents = useUiStore((s) => s.pendingAgents)
  const removePendingAgent = useUiStore((s) => s.removePendingAgent)

  return (
    <div className="flex h-dvh bg-surface-primary overflow-hidden">
      {/* Sidebar */}
      <div className={clsx(
        'transition-all duration-200 overflow-hidden',
        sidebarOpen ? 'w-56' : 'w-0'
      )}>
        {sidebarOpen && <Sidebar />}
      </div>

      {/* Main content */}
      <main className="flex-1 flex flex-col min-w-0 overflow-hidden">
        {pendingAgents.length > 0 && (
          <div className="shrink-0 border-b border-accent/20 bg-accent/10 px-4 py-2 space-y-1">
            {pendingAgents.map((p) => (
              <div key={p.key} className="flex items-center gap-2.5 text-xs text-text-primary">
                <Spinner size="xs" />
                <span className="text-base leading-none">{p.emoji}</span>
                <span className="font-medium">{p.name}</span>
                <span className="text-text-muted">正在创建中</span>
                <span className="text-text-muted truncate">· {p.hint}</span>
                <button
                  type="button"
                  onClick={() => removePendingAgent(p.key)}
                  className="ml-auto text-2xs text-text-muted hover:text-text-primary shrink-0"
                  title="不再提示"
                >
                  忽略
                </button>
              </div>
            ))}
          </div>
        )}
        {view === 'chat' && <ChatCanvas />}
        {view === 'settings' && (
          <div className="flex-1 overflow-hidden">
            <SettingsPanel />
          </div>
        )}
      </main>
    </div>
  )
}

export function _unused(_: ReactNode) {}
