import { type FC, type ReactNode, useState, useEffect, useRef, useId } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import mermaid from 'mermaid'
import { clsx } from 'clsx'
import { Browser } from '@wailsio/runtime'
import type { Message, ToolCall } from '../../types'
import { getToolLabel } from '../../lib/tool-labels'

// Detect Wails3 desktop runtime; in browser dev mode fall back to default link behavior.
const isWailsRuntime = typeof window !== 'undefined' && '_wails' in window

// Open external links in the system default browser instead of the WebView itself.
function handleExternalLink(e: React.MouseEvent<HTMLAnchorElement>, href?: string) {
  if (!href) return
  // Allow in-page anchors to behave normally.
  if (href.startsWith('#')) return
  e.preventDefault()
  if (isWailsRuntime) {
    Browser.OpenURL(href).catch((err) => console.error('[link] OpenURL failed', err))
  } else {
    window.open(href, '_blank', 'noopener,noreferrer')
  }
}

// ---- Mermaid global initialization (runs once) ----
mermaid.initialize({
  startOnLoad: false,
  theme: 'dark',
  fontFamily: 'inherit',
  securityLevel: 'loose',
})

// ---- Mermaid rendering component ----
const MermaidBlock: FC<{ code: string }> = ({ code }) => {
  const id = useId().replace(/:/g, '-')
  const ref = useRef<HTMLDivElement>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    setError(null)
    mermaid.render(`mermaid-${id}`, code).then(({ svg }) => {
      if (!cancelled && ref.current) {
        ref.current.innerHTML = svg
      }
    }).catch((e) => {
      if (!cancelled) setError(String(e?.message ?? e))
    })
    return () => { cancelled = true }
  }, [code, id])

  if (error) {
    return (
      <div className="my-2 rounded-lg border border-error/30 bg-error/5 p-3">
        <p className="text-xs text-error font-mono">Mermaid 渲染失败：{error}</p>
        <pre className="mt-2 text-xs text-text-muted font-mono whitespace-pre-wrap">{code}</pre>
      </div>
    )
  }

  return (
    <div
      ref={ref}
      className="my-3 flex justify-center overflow-x-auto rounded-lg bg-surface-secondary p-4 [&_svg]:max-w-full"
    />
  )
}

interface Props {
  message: Message
  agentEmoji?: string
  isStreaming?: boolean
}

const CodeBlock: FC<{ className?: string; children?: ReactNode }> = ({ className, children }) => {
  const lang = className?.replace('language-', '') ?? ''
  return (
    <div className="my-2 rounded-lg overflow-hidden border border-border/60">
      {lang && (
        <div className="flex items-center justify-between bg-surface-tertiary px-3 py-1.5 border-b border-border/60">
          <span className="text-2xs text-text-muted font-mono">{lang}</span>
          <button
            onClick={() => navigator.clipboard.writeText(String(children))}
            className="text-2xs text-text-muted hover:text-text-primary transition-colors"
          >
            复制
          </button>
        </div>
      )}
      <pre className="bg-surface-primary overflow-x-auto p-3 text-xs font-mono text-text-secondary leading-relaxed">
        <code>{children}</code>
      </pre>
    </div>
  )
}

// Format JSON string; if already a string, try pretty-print
function formatJson(val: unknown): string {
  if (val === undefined || val === null) return ''
  if (typeof val === 'string') {
    try {
      return JSON.stringify(JSON.parse(val), null, 2)
    } catch {
      return val
    }
  }
  return JSON.stringify(val, null, 2)
}

const CopyButton: FC<{ text: string }> = ({ text }) => {
  const [copied, setCopied] = useState(false)
  const copy = () => {
    navigator.clipboard.writeText(text)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }
  return (
    <button
      onClick={copy}
      title="复制"
      className="p-1 rounded hover:bg-white/10 text-text-muted hover:text-text-primary transition-colors"
    >
      {copied ? (
        <svg className="w-3.5 h-3.5 text-success" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
          <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
        </svg>
      ) : (
        <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
          <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
          <path strokeLinecap="round" strokeLinejoin="round" d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1" />
        </svg>
      )}
    </button>
  )
}

// Tool display name mapping — centralized in lib/tool-labels.ts

const ToolCallCard: FC<{ tc: ToolCall }> = ({ tc }) => {
  const isDone = tc.status === 'done' || tc.result !== undefined
  const isStreamingArgs = tc.status === 'streaming' || tc.status === 'ready'
  const isRunning = tc.status === 'running' && !isDone
  // Default expanded while in-flight (streaming/ready/running); collapsed once done.
  const [open, setOpen] = useState(!isDone)
  // Track manual user override so we don't fight the user after they click.
  const userTouched = useRef(false)
  const prevDone = useRef(isDone)

  const argsText = tc.argumentsRaw && (isStreamingArgs || typeof tc.arguments === 'string')
    ? tc.argumentsRaw
    : formatJson(tc.arguments)
  const resultText = formatJson(tc.result)

  // Auto behavior:
  // - while in-flight, keep expanded so user sees streaming args / running state
  // - on transition done(false → true), auto collapse (unless user manually toggled)
  useEffect(() => {
    if (!isDone) {
      if (!userTouched.current) setOpen(true)
    } else if (!prevDone.current && isDone) {
      if (!userTouched.current) setOpen(false)
    }
    prevDone.current = isDone
  }, [isDone])

  const toggle = () => {
    userTouched.current = true
    setOpen(v => !v)
  }

  return (
    <div className="mb-2 px-1 animate-fade-in">
      {/* Title row */}
      <button
        onClick={toggle}
        className="w-full flex items-center gap-2 group"
      >
        {/* Gear icon */}
        <div className={clsx(
          'w-5 h-5 rounded-md flex items-center justify-center shrink-0',
          isDone
            ? tc.isError
              ? 'bg-error/15 border border-error/30'
              : 'bg-success/15 border border-success/30'
            : 'bg-warning/15 border border-warning/30'
        )}>
          {isDone ? (
            tc.isError ? (
              <svg className="w-3 h-3 text-error" fill="none" stroke="currentColor" strokeWidth={2.5} viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
              </svg>
            ) : (
              <svg className="w-3 h-3 text-success" fill="none" stroke="currentColor" strokeWidth={2.5} viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
              </svg>
            )
          ) : (
            <svg className="w-3 h-3 text-warning" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
              <path strokeLinecap="round" strokeLinejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
            </svg>
          )}
        </div>

        {/* Tool name */}
        <span className="text-xs font-mono text-text-secondary group-hover:text-text-primary transition-colors flex-1 text-left truncate">
          {getToolLabel(tc.name)}
          {isStreamingArgs && <span className="ml-2 text-2xs text-text-muted">生成参数中…</span>}
          {isRunning && <span className="ml-2 text-2xs text-warning">执行中…</span>}
          {isDone && !tc.isError && <span className="ml-2 text-2xs text-success/80">已完成</span>}
          {isDone && tc.isError && <span className="ml-2 text-2xs text-error/80">失败</span>}
          {!isDone && (
            <span className="inline-block ml-2 w-2.5 h-2.5 border border-warning/60 border-t-transparent rounded-full animate-spin align-middle" />
          )}
        </span>

        {/* Expand arrow */}
        <svg
          className={clsx('w-3.5 h-3.5 text-text-muted transition-transform duration-200 shrink-0', open && 'rotate-180')}
          fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24"
        >
          <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
        </svg>
      </button>

      {/* Expanded content */}
      {open && (
        <div className="mt-2 ml-7 rounded-lg border border-border/60 overflow-hidden bg-surface-secondary">
          {/* Call arguments */}
          <div className={clsx(isDone && 'border-b border-border/60')}>
            <div className="flex items-center justify-between px-3 py-2 bg-surface-tertiary">
              <span className="text-2xs font-medium text-text-muted">调用参数</span>
              <CopyButton text={argsText} />
            </div>
            <pre className="px-3 py-2.5 text-xs font-mono text-text-secondary leading-relaxed overflow-x-auto max-h-48 whitespace-pre-wrap break-all">
              {argsText || (isStreamingArgs ? '' : '{}')}
              {isStreamingArgs && (
                <span className="inline-block w-1.5 h-3.5 bg-accent/70 rounded-sm align-middle ml-0.5 animate-pulse-soft" />
              )}
            </pre>
          </div>

          {/* Return result */}
          {isDone && <div>
            <div className="flex items-center justify-between px-3 py-2 bg-surface-tertiary border-b border-border/60">
              <span className={clsx('text-2xs font-medium', tc.isError ? 'text-error' : 'text-text-muted')}>
                {tc.isError ? '错误' : '返回结果'}
              </span>
              <CopyButton text={resultText} />
            </div>
            <pre className={clsx(
              'px-3 py-2.5 text-xs font-mono leading-relaxed overflow-x-auto max-h-64',
              tc.isError ? 'text-error/80' : 'text-text-secondary'
            )}>
              {resultText || '(无返回)'}
            </pre>
          </div>}
        </div>
      )}
    </div>
  )
}

export const MessageBubble: FC<Props> = ({ message, agentEmoji = '🤖', isStreaming }) => {
  const isUser = message.role === 'user'
  const isTool = message.role === 'tool'
  const [thinkOpen, setThinkOpen] = useState(false)

  if (isTool && message.tool_calls?.[0]) {
    const tc = message.tool_calls[0]
    return <ToolCallCard tc={tc} />
  }

  // Don't show copy button during streaming to avoid copying incomplete content
  const showCopy = !isStreaming && !message.isThinking && !!(message.content ?? '').trim()

  return (
    <div className={clsx(
      'group/msg flex items-start gap-2.5 mb-4 animate-fade-in',
      isUser && 'flex-row-reverse'
    )}>
      {/* Avatar */}
      {!isUser && (
        <div className="w-7 h-7 rounded-lg bg-surface-tertiary border border-border flex items-center justify-center text-base shrink-0 mt-0.5">
          {agentEmoji}
        </div>
      )}

      {/* Bubble + copy button container */}
      <div className={clsx(
        'relative max-w-[80%] flex items-end gap-1',
        isUser && 'flex-row-reverse'
      )}>
        {/* Bubble */}
        <div className={clsx(
        'rounded-2xl px-4 py-3',
        isUser
          ? 'bg-user-bubble text-white rounded-tr-sm'
          : 'bg-agent-bubble border border-border/60 text-text-primary rounded-tl-sm'
      )}>
        {isUser ? (
          <div>
            {/* File chip list */}
            {message.files && message.files.length > 0 && (
              <div className="flex flex-wrap gap-1 mb-2">
                {message.files.map((p) => {
                  const name = p.replace(/\\/g, '/').split('/').pop() ?? p
                  const isImg = ['.png','.jpg','.jpeg','.gif','.webp','.bmp'].some(e => name.toLowerCase().endsWith(e))
                  return (
                    <span
                      key={p}
                      title={p}
                      className="flex items-center gap-1 bg-white/15 rounded-md px-2 py-0.5 text-xs text-white/90 max-w-[200px]"
                    >
                      <span>{isImg ? '🖼️' : '📄'}</span>
                      <span className="truncate">{name}</span>
                    </span>
                  )
                })}
              </div>
            )}
            <p className="text-sm leading-relaxed whitespace-pre-wrap">{message.content}</p>
          </div>
        ) : (
          <div className={clsx(
            'text-sm leading-relaxed text-text-primary max-w-none',
            // Paragraphs
            '[&_p]:my-1 [&_p]:leading-relaxed',
            // Headings
            '[&_h1]:text-base [&_h2]:text-base [&_h3]:text-sm',
            '[&_h1]:font-semibold [&_h2]:font-semibold [&_h3]:font-semibold',
            '[&_h1]:mt-3 [&_h1]:mb-2 [&_h2]:mt-3 [&_h2]:mb-2 [&_h3]:mt-2 [&_h3]:mb-1',
            // Links: accent color + hover underline (must come after [&_*] to take effect)
            '[&_a]:text-accent [&_a]:underline [&_a]:underline-offset-2 [&_a]:decoration-accent/40 hover:[&_a]:decoration-accent',
            // Lists
            '[&_ul]:my-1 [&_ul]:pl-5 [&_ul]:list-disc',
            '[&_ol]:my-1 [&_ol]:pl-5 [&_ol]:list-decimal',
            '[&_li]:my-0.5',
            // Blockquotes
            '[&_blockquote]:my-2 [&_blockquote]:pl-3 [&_blockquote]:border-l-2 [&_blockquote]:border-accent/40 [&_blockquote]:text-text-muted',
            // Tables (remark-gfm)
            '[&_table]:my-2 [&_table]:w-full [&_table]:border-collapse [&_table]:text-xs',
            '[&_th]:border [&_th]:border-border [&_th]:px-2 [&_th]:py-1 [&_th]:bg-surface-tertiary [&_th]:font-medium [&_th]:text-left',
            '[&_td]:border [&_td]:border-border [&_td]:px-2 [&_td]:py-1',
            // Horizontal rules
            '[&_hr]:my-3 [&_hr]:border-border',
            // Emphasis
            '[&_strong]:font-semibold [&_strong]:text-text-primary',
            '[&_em]:italic'
          )}>
            {/* Thinking block */}
            {(message.thinkContent || message.isThinking) && (
              <div className="not-prose mb-3">
                <button
                  onClick={() => setThinkOpen(v => !v)}
                  className="flex items-center gap-1.5 text-2xs text-text-muted hover:text-text-primary transition-colors group"
                >
                  {message.isThinking ? (
                    <span className="flex items-center gap-1 text-accent/70">
                      <span className="w-2 h-2 rounded-full bg-accent/60 animate-pulse" />
                      思考中…
                    </span>
                  ) : (
                    <span className="flex items-center gap-1">
                      <svg
                        className={clsx('w-3 h-3 transition-transform duration-200', thinkOpen && 'rotate-90')}
                        fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24"
                      >
                        <path strokeLinecap="round" strokeLinejoin="round" d="M9 5l7 7-7 7" />
                      </svg>
                      {thinkOpen ? '收起思考过程' : '查看思考过程'}
                    </span>
                  )}
                </button>

                {/* Always expanded during streaming; after completion, controlled by thinkOpen */}
                {(message.isThinking || thinkOpen) && message.thinkContent && (
                  <div className="mt-1.5 pl-3 border-l-2 border-accent/20">
                    <p className="text-xs text-text-muted leading-relaxed whitespace-pre-wrap font-mono">
                      {message.thinkContent}
                    </p>
                  </div>
                )}
              </div>
            )}

            <ReactMarkdown
              remarkPlugins={[remarkGfm]}
              components={{
                a: ({ href, children }) => (
                  <a
                    href={href}
                    target="_blank"
                    rel="noopener noreferrer"
                    onClick={(e) => handleExternalLink(e, href)}
                    className="text-accent underline hover:text-accent/80 cursor-pointer"
                  >
                    {children}
                  </a>
                ),
                code: ({ className, children }) => {
                  if (className === 'language-mermaid') {
                    return <MermaidBlock code={String(children).replace(/\n$/, '')} />
                  }
                  return className?.includes('language-')
                    ? <CodeBlock className={className}>{children}</CodeBlock>
                    : <code className="text-accent bg-surface-tertiary px-1 py-0.5 rounded text-xs font-mono">{children}</code>
                },
                pre: ({ children }) => <>{children}</>,
              }}
            >
              {message.content ?? ''}
            </ReactMarkdown>
            {/* Generated media (to-image / to-video agents) */}
            {message.media && message.media.length > 0 && (
              <div className="not-prose mt-2 flex flex-col gap-2">
                {message.media.map((m, idx) => {
                  const src = m.url || m.path || ''
                  if (!src) return null
                  if (m.kind === 'video' || (m.mime_type ?? '').startsWith('video/')) {
                    return (
                      <video
                        key={idx}
                        src={src}
                        controls
                        className="rounded-lg max-w-full max-h-96 border border-border/60"
                      />
                    )
                  }
                  return (
                    <img
                      key={idx}
                      src={src}
                      alt={m.name ?? 'generated'}
                      className="rounded-lg max-w-full max-h-96 border border-border/60 object-contain"
                    />
                  )
                })}
              </div>
            )}
            {isStreaming && (
              <span className="inline-block w-2 h-4 bg-accent/70 rounded-sm animate-pulse-soft ml-0.5 align-middle" />
            )}
          </div>
        )}
        </div>

        {/* Floating copy button: appears on hovering the entire message */}
        {showCopy && (
          <div className={clsx(
            'shrink-0 mb-1 opacity-0 group-hover/msg:opacity-100 transition-opacity',
            isUser ? 'mr-1' : 'ml-1'
          )}>
            <CopyButton text={message.content ?? ''} />
          </div>
        )}
      </div>
    </div>
  )
}
