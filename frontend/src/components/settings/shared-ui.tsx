import { clsx } from 'clsx'

export function SectionTitle({ children }: { children: React.ReactNode }) {
  return <h3 className="text-sm font-semibold text-text-primary mb-3">{children}</h3>
}

export function Card({ children, className }: { children: React.ReactNode; className?: string }) {
  return (
    <div className={clsx('bg-surface-secondary rounded-xl border border-border/60 overflow-hidden', className)}>
      {children}
    </div>
  )
}

export function Row({ label, desc, children }: { label: string; desc?: string; children?: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between px-4 py-3 border-b border-border/40 last:border-0">
      <div className="min-w-0 mr-3">
        <p className="text-sm text-text-primary">{label}</p>
        {desc && <p className="text-2xs text-text-muted mt-0.5 truncate">{desc}</p>}
      </div>
      <div className="shrink-0">{children}</div>
    </div>
  )
}

export function Toggle({ value, onChange }: { value: boolean; onChange: (v: boolean) => void }) {
  return (
    <button onClick={() => onChange(!value)}
      className={clsx('w-9 h-5 rounded-full transition-colors relative', value ? 'bg-accent' : 'bg-surface-tertiary')}>
      <span className={clsx('absolute top-0.5 w-4 h-4 rounded-full bg-white shadow-sm transition-transform',
        value ? 'translate-x-4' : 'translate-x-0.5')} />
    </button>
  )
}

export function Badge({ color, children }: { color: 'green' | 'orange' | 'gray'; children: React.ReactNode }) {
  const cls = { green: 'bg-success/15 text-success', orange: 'bg-warning/15 text-warning', gray: 'bg-surface-tertiary text-text-muted' }[color]
  return <span className={clsx('text-2xs px-1.5 py-0.5 rounded-md font-medium', cls)}>{children}</span>
}
