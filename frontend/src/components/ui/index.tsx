import { type FC } from 'react'
import { clsx } from 'clsx'

interface SpinnerProps { size?: 'xs' | 'sm' | 'md'; className?: string }

export const Spinner: FC<SpinnerProps> = ({ size = 'sm', className }) => {
  const sz = size === 'xs' ? 'w-3 h-3' : size === 'sm' ? 'w-4 h-4' : 'w-6 h-6'
  return (
    <div
      className={clsx(
        sz, 'rounded-full border-2 border-accent border-t-transparent animate-spin',
        className
      )}
    />
  )
}

interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: 'primary' | 'secondary' | 'ghost' | 'danger'
  size?: 'xs' | 'sm' | 'md'
  loading?: boolean
}

export const Button: FC<ButtonProps> = ({
  variant = 'secondary', size = 'sm', loading, children, className, disabled, ...props
}) => {
  const base = 'inline-flex items-center gap-1.5 font-medium rounded-lg transition-colors disabled:opacity-50 disabled:cursor-not-allowed'
  const variants = {
    primary: 'bg-accent text-white hover:bg-accent-hover',
    secondary: 'border border-border text-text-secondary hover:bg-surface-tertiary hover:text-text-primary',
    ghost: 'text-text-secondary hover:bg-surface-tertiary hover:text-text-primary',
    danger: 'border border-error/40 text-error hover:bg-error/10',
  }
  const sizes = {
    xs: 'px-2.5 py-1 text-xs',
    sm: 'px-3 py-1.5 text-xs',
    md: 'px-4 py-2 text-sm',
  }
  return (
    <button
      className={clsx(base, variants[variant], sizes[size], className)}
      disabled={disabled || loading}
      {...props}
    >
      {loading && <Spinner size="xs" />}
      {children}
    </button>
  )
}

interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {
  label?: string
  error?: string
}

export const Input: FC<InputProps> = ({ label, error, className, ...props }) => (
  <div className="space-y-1">
    {label && <label className="block text-xs font-medium text-text-secondary">{label}</label>}
    <input
      className={clsx(
        'w-full bg-surface-tertiary border rounded-lg px-3 py-2 text-sm text-text-primary',
        'placeholder:text-text-muted focus:outline-none focus:ring-1 focus:ring-accent',
        error ? 'border-error' : 'border-border',
        className,
      )}
      {...props}
    />
    {error && <p className="text-xs text-error">{error}</p>}
  </div>
)

interface TextareaProps extends React.TextareaHTMLAttributes<HTMLTextAreaElement> {
  label?: string
  error?: string
}

export const Textarea: FC<TextareaProps> = ({ label, error, className, ...props }) => (
  <div className="space-y-1">
    {label && <label className="block text-xs font-medium text-text-secondary">{label}</label>}
    <textarea
      className={clsx(
        'w-full bg-surface-tertiary border rounded-lg px-3 py-2 text-sm text-text-primary',
        'placeholder:text-text-muted focus:outline-none focus:ring-1 focus:ring-accent resize-y',
        error ? 'border-error' : 'border-border',
        className,
      )}
      {...props}
    />
    {error && <p className="text-xs text-error">{error}</p>}
  </div>
)

export const Divider: FC<{ className?: string }> = ({ className }) => (
  <div className={clsx('border-t border-border', className)} />
)
