import { useState } from 'react'
import { clsx } from 'clsx'
import { useUiStore } from '../../stores/ui-store'
import { AppearanceTab } from './AppearanceTab'
import { AgentsTab } from './AgentsTab'
import { ProjectsTab } from './ProjectsTab'
import { ModelsTab } from './ModelsTab'
import { ChannelsTab } from './ChannelsTab'
import { CronTab } from './CronTab'
import { McpTab } from './McpTab'
import { SkillsTab } from './SkillsTab'
import { CredentialsTab } from './CredentialsTab'
import { ResourcesTab } from './ResourcesTab'

// ─── Tabs ─────────────────────────────────────────────────────────────────────

type TabKey = 'appearance' | 'agents' | 'projects' | 'models' | 'channels' | 'cron' | 'mcp' | 'skills' | 'credentials' | 'resources'

const TABS: { key: TabKey; label: string; icon: string }[] = [
  { key: 'appearance', label: '外观', icon: '🎨' },
  { key: 'models',     label: '模型',    icon: '🧠' },
  { key: 'agents',     label: 'Agent',   icon: '🤖' },
  { key: 'projects',   label: '项目',    icon: '📁' },
  { key: 'channels',   label: 'Channel', icon: '📡' },
  { key: 'cron',       label: '定时',    icon: '⏰' },
  { key: 'mcp',        label: 'MCP',     icon: '🔌' },
  { key: 'skills',     label: 'Skill',   icon: '⚡' },
  { key: 'credentials', label: '凭证', icon: '🔑' },
  { key: 'resources',   label: '资源', icon: '🖼️' },
]

// ─── Main ─────────────────────────────────────────────────────────────────────

export function SettingsPanel() {
  const [tab, setTab] = useState<TabKey>('agents')
  const setView = useUiStore((s) => s.setView)

  const tabContent: Record<TabKey, React.ReactNode> = {
    appearance: <AppearanceTab />,
    agents:     <AgentsTab />,
    projects:   <ProjectsTab />,
    models:     <ModelsTab />,
    channels:   <ChannelsTab />,
    cron:       <CronTab />,
    mcp:        <McpTab />,
    skills:     <SkillsTab />,
    credentials: <CredentialsTab />,
    resources:  <ResourcesTab />,
  }

  return (
    <div className="flex h-full bg-surface-primary">
      {/* Left nav */}
      <div className="w-44 shrink-0 border-r border-border/60 flex flex-col py-4 px-2 gap-0.5">
        <p className="text-2xs font-semibold text-text-muted uppercase tracking-wider px-3 mb-2">设置</p>
        {TABS.map((t) => (
          <button key={t.key} onClick={() => setTab(t.key)}
            className={clsx(
              'flex items-center gap-2.5 px-3 py-2 rounded-lg text-sm transition-colors text-left',
              tab === t.key
                ? 'bg-accent/15 text-accent'
                : 'text-text-secondary hover:bg-surface-tertiary hover:text-text-primary'
            )}>
            <span>{t.icon}</span>
            <span>{t.label}</span>
          </button>
        ))}
      </div>

      {/* Content */}
      <div className="flex-1 overflow-y-auto relative">
        {/* Close button */}
        <button
          onClick={() => setView('chat')}
          title="关闭设置"
          className="absolute top-4 right-4 z-10 w-7 h-7 flex items-center justify-center rounded-lg text-text-muted hover:text-text-primary hover:bg-surface-tertiary transition-colors"
        >
          <svg className="w-4 h-4" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
          </svg>
        </button>
        <div className="max-w-4xl mx-auto px-8 py-6">
          {tabContent[tab]}
        </div>
      </div>
    </div>
  )
}
