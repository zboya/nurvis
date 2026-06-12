import { clsx } from 'clsx'
import { useUiStore } from '../../stores/ui-store'
import { SectionTitle } from './shared-ui'

export function AppearanceTab() {
  const { theme, setTheme } = useUiStore()
  return (
    <div className="space-y-6">
      <div>
        <SectionTitle>主题</SectionTitle>
        <div className="flex gap-3">
          {[{ key: 'dark' as const, label: '深色', icon: '🌙' }, { key: 'light' as const, label: '浅色', icon: '☀️' }].map((t) => (
            <button key={t.key} onClick={() => setTheme(t.key)}
              className={clsx('flex-1 flex flex-col items-center gap-2 py-4 rounded-xl border-2 transition-all',
                theme === t.key
                  ? 'border-accent bg-accent/10 text-accent'
                  : 'border-border/60 bg-surface-secondary text-text-secondary hover:border-border hover:text-text-primary')}>
              <span className="text-2xl">{t.icon}</span>
              <span className="text-xs font-medium">{t.label}</span>
            </button>
          ))}
        </div>
      </div>
    </div>
  )
}
