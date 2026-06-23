import { useEffect, useState, useCallback } from 'react'
import { Browser } from '@wailsio/runtime'
import { getWs } from '../../lib/ws'
import { SelectDirectory } from '../../../bindings/github.com/zboya/nurvis/cmd/nurvis-desktop/service'
import { Button, Input } from '../ui'
import { SectionTitle, Card } from './shared-ui'

interface FsEntry {
  name: string
  path: string
  is_dir: boolean
  size: number
  modified_ms: number
  url?: string
}

interface ListDirResult {
  path: string
  exists: boolean
  entries: FsEntry[]
}

const IMAGE_EXTS = ['.png', '.jpg', '.jpeg', '.webp', '.gif', '.bmp']

function isImage(name: string): boolean {
  const lower = name.toLowerCase()
  return IMAGE_EXTS.some((e) => lower.endsWith(e))
}

function humanSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`
  return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB`
}

function formatTime(ms: number): string {
  if (!ms) return ''
  const d = new Date(ms)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

export function ResourcesTab() {
  const ws = getWs()

  const [currentDir, setCurrentDir] = useState('')
  const [inputDir, setInputDir] = useState('')
  const [browseDir, setBrowseDir] = useState('')
  const [entries, setEntries] = useState<FsEntry[]>([])
  const [exists, setExists] = useState(true)
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [hint, setHint] = useState('')

  // Load current configured dir + saved setting on mount.
  useEffect(() => {
    let cancelled = false
    Promise.all([
      ws.call<{ path: string }>('fs.media_output_dir'),
      ws.call<{ key: string; value: unknown }>('settings.get', { key: 'media_output_dir' }),
    ])
      .then(([rt, st]) => {
        if (cancelled) return
        const effective = rt.path || ''
        const saved = typeof st.value === 'string' ? st.value : ''
        setCurrentDir(effective)
        setInputDir(saved || effective)
        setBrowseDir(effective)
      })
      .catch(() => {
        if (!cancelled) setError('加载当前路径失败')
      })
    return () => {
      cancelled = true
    }
  }, [ws])

  const loadDir = useCallback(
    async (dir: string) => {
      setLoading(true)
      setError('')
      try {
        const res = await ws.call<ListDirResult>('fs.list_dir', { path: dir })
        setBrowseDir(res.path)
        setEntries(res.entries ?? [])
        setExists(res.exists)
      } catch (e) {
        setError(e instanceof Error ? e.message : '读取目录失败')
        setEntries([])
        setExists(false)
      } finally {
        setLoading(false)
      }
    },
    [ws]
  )

  // Whenever browseDir is set (after mount or save), refresh listing.
  useEffect(() => {
    if (browseDir) loadDir(browseDir)
  }, [browseDir, loadDir])

  const onSave = async () => {
    const dir = inputDir.trim()
    setSaving(true)
    setError('')
    setHint('')
    try {
      await ws.call('settings.set', { key: 'media_output_dir', value: dir })
      // Reload effective dir from server (handles fallback when empty).
      const rt = await ws.call<{ path: string }>('fs.media_output_dir')
      setCurrentDir(rt.path)
      setBrowseDir(rt.path)
      setHint('已保存')
      setTimeout(() => setHint(''), 2000)
    } catch (e) {
      setError(e instanceof Error ? e.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const onReset = () => {
    setInputDir('')
  }

  const onBrowse = async () => {
    try {
      const picked = await SelectDirectory()
      if (picked) setInputDir(picked)
    } catch (e) {
      setError(e instanceof Error ? e.message : '选择目录失败')
    }
  }

  return (
    <div className="space-y-6">
      <div>
        <SectionTitle>图片保存路径</SectionTitle>
        <Card className="p-4 space-y-3">
          <div>
            <label className="block text-xs font-medium text-text-secondary mb-1">目录</label>
            <div className="relative">
              <Input
                className="pr-10"
                placeholder="/absolute/path/to/outputs（留空使用默认 ~/.nurvis/outputs）"
                value={inputDir}
                onChange={(e) => setInputDir(e.target.value)}
              />
              <button
                type="button"
                onClick={onBrowse}
                title="选择本地目录"
                className="absolute right-1.5 top-1/2 -translate-y-1/2 w-7 h-7 flex items-center justify-center rounded-md text-text-muted hover:text-text-primary hover:bg-surface-secondary transition-colors"
              >
                <svg className="w-4 h-4" fill="none" stroke="currentColor" strokeWidth={1.8} viewBox="0 0 24 24">
                  <path
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    d="M3 7a2 2 0 0 1 2-2h3.6a2 2 0 0 1 1.4.6L11.4 7H19a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z"
                  />
                </svg>
              </button>
            </div>
            <p className="text-2xs text-text-muted mt-1">
              当前生效：<span className="font-mono">{currentDir || '（未配置）'}</span>
            </p>
          </div>
          <div className="flex items-center gap-2">
            <Button variant="primary" loading={saving} onClick={onSave}>保存</Button>
            <Button variant="secondary" onClick={onReset}>恢复默认</Button>
            {hint && <span className="text-2xs text-success">{hint}</span>}
            {error && <span className="text-2xs text-error">{error}</span>}
          </div>
        </Card>
      </div>

      <div>
        <div className="flex items-center justify-between mb-3">
          <SectionTitle>目录内容</SectionTitle>
          <button
            onClick={() => browseDir && loadDir(browseDir)}
            className="text-xs text-accent hover:underline"
          >
            刷新
          </button>
        </div>
        <Card>
          {loading ? (
            <div className="px-4 py-8 text-center text-xs text-text-muted">加载中…</div>
          ) : !exists ? (
            <div className="px-4 py-8 text-center text-xs text-text-muted">
              目录不存在或暂无内容：<span className="font-mono">{browseDir}</span>
            </div>
          ) : entries.length === 0 ? (
            <div className="px-4 py-8 text-center text-xs text-text-muted">该目录为空</div>
          ) : (
            <ul className="divide-y divide-border/40 max-h-[480px] overflow-y-auto">
              {entries.map((e) => (
                <li key={e.path} className="flex items-center gap-3 px-4 py-2.5">
                  <div className="w-10 h-10 shrink-0 rounded-md bg-surface-tertiary border border-border/40 overflow-hidden flex items-center justify-center text-base">
                    {e.is_dir ? (
                      <span>📁</span>
                    ) : isImage(e.name) && e.url ? (
                      // eslint-disable-next-line jsx-a11y/alt-text
                      <img src={e.url} className="w-full h-full object-cover" />
                    ) : (
                      <span>📄</span>
                    )}
                  </div>
                  <div className="min-w-0 flex-1">
                    <p className="text-xs text-text-primary truncate" title={e.path}>
                      {e.name}
                    </p>
                    <p className="text-2xs text-text-muted">
                      {e.is_dir ? '目录' : humanSize(e.size)} · {formatTime(e.modified_ms)}
                    </p>
                  </div>
                  {!e.is_dir && e.url && (
                    <button
                      type="button"
                      onClick={() => {
                        if (!e.url) return
                        Browser.OpenURL(e.url).catch((err) =>
                          console.error('[resources] OpenURL failed', err),
                        )
                      }}
                      className="text-2xs text-accent hover:underline shrink-0"
                    >
                      打开
                    </button>
                  )}
                </li>
              ))}
            </ul>
          )}
        </Card>
      </div>
    </div>
  )
}
