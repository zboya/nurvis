import { type FC } from 'react'
import { clsx } from 'clsx'

// AgentTagBadge — small pill rendered next to an agent's name to show its
// runtime modality (text / image / video). Source of truth: agent.tag, which
// is computed by the backend from the model's HuggingFace metadata when the
// agent is created.
//
// Designed to stay compact: an emoji + short Chinese label, with color hints
// so the modality is recognisable at a glance even when the surrounding text
// is dense (chat top bar, agent list, sidebar).

export type AgentTag = 'to-text' | 'to-image' | 'to-video' | string

interface Style {
  label: string
  emoji: string
  className: string
}

// to-text intentionally returns null so we don't render a "默认" badge for
// every conversation — the chat top bar would feel noisy. Callers should
// short-circuit on null.
function styleFor(tag?: AgentTag): Style | null {
  switch (tag) {
    case 'to-image':
      return {
        label: '文生图',
        emoji: '🎨',
        className: 'bg-fuchsia-500/15 text-fuchsia-300 border-fuchsia-500/30',
      }
    case 'to-video':
      return {
        label: '文生视频',
        emoji: '🎬',
        className: 'bg-cyan-500/15 text-cyan-300 border-cyan-500/30',
      }
    default:
      return null
  }
}

interface Props {
  tag?: AgentTag
  size?: 'sm' | 'xs'
  className?: string
}

export const AgentTagBadge: FC<Props> = ({ tag, size = 'sm', className }) => {
  const s = styleFor(tag)
  if (!s) return null
  const sizing =
    size === 'xs'
      ? 'text-[10px] px-1.5 py-0 leading-4'
      : 'text-2xs px-2 py-0.5 leading-tight'
  return (
    <span
      className={clsx(
        'inline-flex items-center gap-1 rounded-full border font-medium whitespace-nowrap',
        s.className,
        sizing,
        className,
      )}
      title={`Agent 类型：${s.label}`}
    >
      <span>{s.emoji}</span>
      <span>{s.label}</span>
    </span>
  )
}
