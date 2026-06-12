import { type ReactNode } from 'react'
import { Sidebar } from './Sidebar'
import { useUiStore } from '../../stores/ui-store'
import { ChatCanvas } from '../chat/ChatCanvas'
import { SettingsPanel } from '../settings/SettingsPanel'
import { clsx } from 'clsx'

export function AppShell() {
  const view = useUiStore((s) => s.view)
  const sidebarOpen = useUiStore((s) => s.sidebarOpen)

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
