import { type FC, useState, useRef, useCallback, useEffect } from 'react'
import { clsx } from 'clsx'
import { SelectFiles } from '../../../bindings/github.com/zboya/nurvis/cmd/nurvis-desktop/service'

export interface AttachedFile {
  path: string
  name: string
  isImage: boolean
}

interface Props {
  onSend: (text: string, files: string[]) => void
  onStop: () => void
  isRunning: boolean
  disabled?: boolean
  placeholder?: string
  /** Whether the current agent's model supports vision. undefined means not yet probed. */
  visionSupported?: boolean
  /** Vision capability probing in progress. */
  visionLoading?: boolean
}

const IMAGE_EXTS = ['.png', '.jpg', '.jpeg', '.gif', '.webp', '.bmp']

function isImagePath(p: string) {
  const lower = p.toLowerCase()
  return IMAGE_EXTS.some((ext) => lower.endsWith(ext))
}

function basename(p: string) {
  const idx = Math.max(p.lastIndexOf('/'), p.lastIndexOf('\\'))
  return idx >= 0 ? p.slice(idx + 1) : p
}

export const InputBar: FC<Props> = ({
  onSend, onStop, isRunning, disabled, placeholder, visionSupported, visionLoading,
}) => {
  const [text, setText] = useState('')
  const [files, setFiles] = useState<AttachedFile[]>([])
  const [warn, setWarn] = useState<string | null>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  // Auto-resize textarea
  useEffect(() => {
    const el = textareaRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = Math.min(el.scrollHeight, 160) + 'px'
  }, [text])

  // When model changes (visionSupported changes), auto-remove unsupported image attachments and notify
  useEffect(() => {
    if (visionSupported === false && files.some((f) => f.isImage)) {
      const remaining = files.filter((f) => !f.isImage)
      setFiles(remaining)
      setWarn('当前模型不支持图片，已自动移除已选图片附件')
    }
  }, [visionSupported, files])

  const handlePickFiles = useCallback(async () => {
    setWarn(null)
    try {
      const picked = await SelectFiles()
      if (!picked || picked.length === 0) return
      const next: AttachedFile[] = []
      let blockedImage = false
      for (const p of picked) {
        const isImage = isImagePath(p)
        if (isImage && visionSupported === false) {
          blockedImage = true
          continue
        }
        next.push({ path: p, name: basename(p), isImage })
      }
      if (blockedImage) {
        setWarn('当前模型不支持图片，已忽略图片类附件')
      }
      // Merge with existing attachments, deduplicating by path
      setFiles((prev) => {
        const seen = new Set(prev.map((f) => f.path))
        const merged = [...prev]
        for (const f of next) {
          if (!seen.has(f.path)) merged.push(f)
        }
        return merged
      })
    } catch (e) {
      console.error('select files failed', e)
    }
  }, [visionSupported])

  const removeFile = useCallback((path: string) => {
    setFiles((prev) => prev.filter((f) => f.path !== path))
  }, [])

  const handleSend = useCallback(() => {
    const t = text.trim()
    if ((!t && files.length === 0) || disabled || isRunning) return
    onSend(t, files.map((f) => f.path))
    setText('')
    setFiles([])
    setWarn(null)
  }, [text, files, disabled, isRunning, onSend])

  const handleKey = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
  }, [handleSend])

  const canSend = (text.trim().length > 0 || files.length > 0) && !disabled

  return (
    <div className="px-4 py-3 bg-surface-primary/80 backdrop-blur-sm max-w-3xl mx-auto w-full">
      {/* File chip list */}
      {files.length > 0 && (
        <div className="flex flex-wrap gap-1.5 mb-2">
          {files.map((f) => (
            <div
              key={f.path}
              className="flex items-center gap-1.5 bg-surface-secondary border border-border rounded-lg px-2 py-1 text-xs text-text-secondary max-w-[240px]"
              title={f.path}
            >
              <span>{f.isImage ? '🖼️' : '📄'}</span>
              <span className="truncate">{f.name}</span>
              <button
                type="button"
                onClick={() => removeFile(f.path)}
                className="text-text-muted hover:text-error transition-colors shrink-0"
                title="移除"
              >
                <svg className="w-3 h-3" fill="none" stroke="currentColor" strokeWidth={2.5} viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" d="M6 6l12 12M6 18L18 6" />
                </svg>
              </button>
            </div>
          ))}
        </div>
      )}

      {/* Warning / capability hint */}
      {warn && (
        <p className="text-2xs text-warning mb-1.5">{warn}</p>
      )}

      <div className={clsx(
        'flex items-end gap-2 bg-surface-secondary border rounded-2xl px-3 py-2 transition-all',
        disabled ? 'border-border opacity-60' : 'border-border focus-within:border-accent/50 focus-within:shadow-focus'
      )}>
        {/* Attachment button */}
        <button
          type="button"
          onClick={handlePickFiles}
          disabled={disabled || isRunning}
          className="w-8 h-8 rounded-xl bg-surface-tertiary border border-border text-text-secondary hover:text-accent hover:border-accent/40 transition-colors flex items-center justify-center shrink-0 disabled:opacity-40"
          title={
            visionLoading
              ? '正在探测模型能力…'
              : visionSupported === false
                ? '添加附件（当前模型不支持图片）'
                : '添加附件（图片 / 文本 / 代码）'
          }
        >
          <svg className="w-4 h-4" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round"
              d="M21.44 11.05l-9.19 9.19a6 6 0 01-8.49-8.49l9.19-9.19a4 4 0 015.66 5.66l-9.2 9.19a2 2 0 01-2.83-2.83l8.49-8.48" />
          </svg>
        </button>

        <textarea
          ref={textareaRef}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={handleKey}
          disabled={disabled || isRunning}
          placeholder={placeholder ?? '发消息…（Enter 发送，Shift+Enter 换行）'}
          rows={1}
          className="flex-1 resize-none bg-transparent text-sm text-text-primary placeholder:text-text-muted focus:outline-none leading-relaxed py-1 min-h-[22px]"
        />
        {isRunning ? (
          <button
            onClick={onStop}
            className="w-8 h-8 rounded-xl bg-error/15 border border-error/30 text-error hover:bg-error/25 transition-colors flex items-center justify-center shrink-0"
            title="停止"
          >
            <svg className="w-3.5 h-3.5" fill="currentColor" viewBox="0 0 24 24">
              <rect x="6" y="6" width="12" height="12" rx="2" />
            </svg>
          </button>
        ) : (
          <button
            onClick={handleSend}
            disabled={!canSend}
            className={clsx(
              'w-8 h-8 rounded-xl flex items-center justify-center shrink-0 transition-all',
              canSend
                ? 'bg-accent text-white hover:bg-accent-hover shadow-md'
                : 'bg-surface-tertiary text-text-muted'
            )}
            title="发送 (Enter)"
          >
            <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth={2.5} viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 19V5m-7 7l7-7 7 7" />
            </svg>
          </button>
        )}
      </div>
      <p className="text-2xs text-text-muted text-center mt-1.5">AI 可能会犯错，请核实重要信息</p>
    </div>
  )
}
