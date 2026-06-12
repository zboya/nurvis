import { useState, useEffect, useRef } from 'react'
import { clsx } from 'clsx'
import { getWs } from '../../lib/ws'
import { useModelStore, type ModelState } from '../../stores/model-store'
import { SectionTitle, Card, Badge } from './shared-ui'
import type { ModelInfo, LibraryModel, RepoFile } from './types'

// ─── Pull progress helpers ───────────────────────────────────────────────

function translatePullStatus(s?: string): string {
  if (!s) return '连接中…'
  const lower = s.toLowerCase()
  if (lower.includes('queued')) return '排队中…'
  if (lower.includes('pulling manifest')) return '获取模型清单…'
  if (lower.includes('pulling model') || lower.includes('pulling')) return '下载中'
  if (lower.includes('verifying')) return '校验完整性…'
  if (lower.includes('writing manifest')) return '写入清单…'
  if (lower.includes('removing')) return '清理临时文件…'
  if (lower.includes('success')) return '下载完成'
  return s
}

function fmtPullBytes(n: number): string {
  if (!n || n <= 0) return '0 B'
  if (n >= 1e9) return `${(n / 1e9).toFixed(2)} GB`
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)} MB`
  if (n >= 1e3) return `${(n / 1e3).toFixed(0)} KB`
  return `${n} B`
}

function PullProgressCard({ entry, onDismiss, onRetry }: { entry: ModelState; onDismiss: () => void; onRetry?: () => void }) {
  const pct = Math.max(0, Math.min(100, Math.round(entry.percent ?? 0)))
  const isError = !!entry.error && !entry.interrupted
  const isInterrupted = !!entry.interrupted
  const isDone = entry.done && !isError && !isInterrupted
  const isFailed = isError || isInterrupted

  return (
    <div className={clsx(
      'rounded-xl border px-3.5 py-2.5',
      isError ? 'border-error/40 bg-error/5'
        : isInterrupted ? 'border-amber-500/40 bg-amber-500/5'
          : isDone ? 'border-success/30 bg-success/5'
            : 'border-border bg-surface-secondary/60',
    )}>
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-2.5 min-w-0">
          {isError ? (
            <div className="w-6 h-6 rounded-full bg-error/15 flex items-center justify-center shrink-0">
              <svg className="w-3.5 h-3.5 text-error" fill="none" stroke="currentColor" strokeWidth={2.5} viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
              </svg>
            </div>
          ) : isInterrupted ? (
            <div className="w-6 h-6 rounded-full bg-amber-500/15 flex items-center justify-center shrink-0">
              <svg className="w-3.5 h-3.5 text-amber-500" fill="none" stroke="currentColor" strokeWidth={2.5} viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" d="M10 9v6m4-6v6M5 5h14a2 2 0 012 2v10a2 2 0 01-2 2H5a2 2 0 01-2-2V7a2 2 0 012-2z" />
              </svg>
            </div>
          ) : isDone ? (
            <div className="w-6 h-6 rounded-full bg-success/20 flex items-center justify-center shrink-0">
              <svg className="w-3.5 h-3.5 text-success" fill="none" stroke="currentColor" strokeWidth={2.5} viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
              </svg>
            </div>
          ) : (
            <span className="relative flex h-2.5 w-2.5 shrink-0 ml-1.5 mr-1">
              <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-accent opacity-60" />
              <span className="relative inline-flex h-2.5 w-2.5 rounded-full bg-accent" />
            </span>
          )}
          <div className="min-w-0">
            <p className="text-sm font-medium text-text-primary truncate">{entry.model}</p>
            <p className={clsx(
              'text-2xs mt-0.5 truncate',
              isError ? 'text-error'
                : isInterrupted ? 'text-amber-500'
                  : isDone ? 'text-success' : 'text-text-muted',
            )}>
              {isError ? entry.error
                : isInterrupted ? (entry.error || '已中断（应用重启），可点继续下载')
                  : translatePullStatus(entry.status)}
            </p>
          </div>
        </div>
        <div className="flex items-center gap-3 shrink-0">
          {!isFailed && !isDone && (
            <div className="text-right">
              <p className="text-sm font-semibold text-text-primary tabular-nums">{pct}%</p>
              {entry.total > 0 && (
                <p className="text-2xs text-text-muted tabular-nums mt-0.5">
                  {fmtPullBytes(entry.current)} / {fmtPullBytes(entry.total)}
                </p>
              )}
            </div>
          )}
          {isFailed && onRetry && (
            <button
              onClick={onRetry}
              className="text-xs px-2.5 py-1 bg-accent/10 hover:bg-accent/20 text-accent rounded-md transition-colors"
              title="继续下载"
            >
              继续下载
            </button>
          )}
          {(isDone || isFailed) && (
            <button
              onClick={onDismiss}
              className="text-text-muted hover:text-text-primary p-1 transition-colors"
              title="关闭"
            >
              <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          )}
        </div>
      </div>

      {!isFailed && !isDone && (
        <div className="relative mt-2 h-1.5 w-full overflow-hidden rounded-full bg-surface-tertiary">
          {pct > 0 ? (
            <div
              className="relative h-full rounded-full progress-gradient transition-[width] duration-500 ease-out"
              style={{ width: `${pct}%` }}
            >
              <div
                className="absolute inset-0 rounded-full opacity-40"
                style={{
                  backgroundImage:
                    'linear-gradient(45deg, rgba(255,255,255,0.25) 25%, transparent 25%, transparent 50%, rgba(255,255,255,0.25) 50%, rgba(255,255,255,0.25) 75%, transparent 75%, transparent)',
                  backgroundSize: '1rem 1rem',
                  animation: 'progress-stripes 1s linear infinite',
                }}
              />
            </div>
          ) : (
            <div
              className="absolute top-0 bottom-0 w-1/3 rounded-full progress-gradient opacity-70"
              style={{ left: '-35%', animation: 'progress-indeterminate 1.4s ease-in-out infinite' }}
            />
          )}
        </div>
      )}
    </div>
  )
}

// ─── ModelsTab ───────────────────────────────────────────────────────────

export function ModelsTab() {
  const ws = getWs()
  const [models, setModels] = useState<ModelInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [pullInput, setPullInput] = useState('')
  const [recommended, setRecommended] = useState<string[]>([])
  const [deleting, setDeleting] = useState<string | null>(null)
  const [pullError, setPullError] = useState<string | null>(null)

  // Search → select repo → select file flow.
  const [searchResults, setSearchResults] = useState<LibraryModel[]>([])
  const [searching, setSearching] = useState(false)
  const [searchError, setSearchError] = useState<string | null>(null)
  const [selectedRepo, setSelectedRepo] = useState<string | null>(null)
  const [repoFiles, setRepoFiles] = useState<RepoFile[]>([])
  const [loadingFiles, setLoadingFiles] = useState(false)
  const [selectedFile, setSelectedFile] = useState<string | null>(null)
  const searchTimer = useRef<number | null>(null)
  const searchSeq = useRef(0)

  // Pull progress is tracked globally so it survives this tab being unmounted.
  const pulls = useModelStore((s) => s.pulls)
  const startPull = useModelStore((s) => s.start)
  const failPull = useModelStore((s) => s.fail)
  const dismissPull = useModelStore((s) => s.dismiss)
  const pullEntries = Object.values(pulls).sort((a, b) => a.startedAt - b.startedAt)
  const hasActivePull = pullEntries.some((p) => !p.done && !p.error && !p.interrupted)

  const loadModels = () => {
    ws.call<{ models: ModelInfo[] }>('models.list')
      .then((r) => setModels(r.models ?? []))
      .catch(() => setModels([]))
      .finally(() => setLoading(false))
  }

  useEffect(() => {
    loadModels()
    ws.call<{ recommended: string[] }>('models.recommend')
      .then((r) => setRecommended(r.recommended ?? []))
      .catch(() => { })
  }, [ws])

  // Auto-refresh installed model list whenever a pull just completed.
  useEffect(() => {
    if (pullEntries.some((p) => p.done && p.finishedAt && Date.now() - p.finishedAt < 2000)) {
      loadModels()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pullEntries.map((p) => `${p.model}:${p.done}`).join('|')])

  const handlePull = async () => {
    if (!selectedFile || !selectedRepo) return
    const name = `${selectedRepo}/${selectedFile}`
    if (pulls[name] && !pulls[name].done && !pulls[name].error && !pulls[name].interrupted) {
      return
    }
    setPullError(null)
    startPull(name, selectedRepo, selectedFile)
    try {
      await ws.call('models.pull', { repo: selectedRepo, file: selectedFile })
      // Reset the search/select state so user can pick another model.
      setPullInput('')
      setSearchResults([])
      setSelectedRepo(null)
      setRepoFiles([])
      setSelectedFile(null)
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      failPull(name, msg)
      setPullError(msg)
    }
  }

  // Run a HuggingFace search via models.library. Cancels previous in-flight result with seq guard.
  const runSearch = async (term: string) => {
    const q = term.trim()
    setSearchError(null)
    if (!q) {
      setSearchResults([])
      setSearching(false)
      return
    }
    const seq = ++searchSeq.current
    setSearching(true)
    try {
      const r = await ws.call<{ library: LibraryModel[] }>('models.library', { Search: q, Limit: 20 })
      if (seq !== searchSeq.current) return
      setSearchResults(r.library ?? [])
    } catch (e) {
      if (seq !== searchSeq.current) return
      setSearchError(e instanceof Error ? e.message : String(e))
      setSearchResults([])
    } finally {
      if (seq === searchSeq.current) setSearching(false)
    }
  }

  // Debounced search on input change while no file is selected yet.
  const onInputChange = (val: string) => {
    setPullInput(val)
    // Any input edit invalidates a previous file selection (user is re-searching).
    if (selectedFile || selectedRepo) {
      setSelectedFile(null)
      setSelectedRepo(null)
      setRepoFiles([])
    }
    if (searchTimer.current) {
      window.clearTimeout(searchTimer.current)
      searchTimer.current = null
    }
    const q = val.trim()
    if (!q) {
      searchSeq.current++
      setSearchResults([])
      setSearching(false)
      return
    }
    searchTimer.current = window.setTimeout(() => runSearch(q), 350)
  }

  const selectRepo = async (repo: string) => {
    setSelectedRepo(repo)
    setSelectedFile(null)
    setRepoFiles([])
    setSearchError(null)
    setLoadingFiles(true)
    try {
      const r = await ws.call<{ files: RepoFile[] }>('models.repo_files', { repo })
      setRepoFiles(r.files ?? [])
    } catch (e) {
      setSearchError(e instanceof Error ? e.message : String(e))
    } finally {
      setLoadingFiles(false)
    }
  }

  const [confirmDelete, setConfirmDelete] = useState<string | null>(null)

  const handleDelete = async (model: string) => {
    if (confirmDelete !== model) {
      setConfirmDelete(model)
      // Auto-cancel confirm state after 3 seconds
      setTimeout(() => setConfirmDelete((cur) => cur === model ? null : cur), 3000)
      return
    }
    setConfirmDelete(null)
    setDeleting(model)
    try {
      await ws.call('models.delete', { model })
      loadModels()
    } catch { /* ignore */ }
    setDeleting(null)
  }

  const fmtSize = (b?: number) => {
    if (!b) return ''
    if (b > 1e9) return `${(b / 1e9).toFixed(1)} GB`
    return `${(b / 1e6).toFixed(0)} MB`
  }

  const capLabel: Record<string, string> = {
    chat: '对话', // legacy value compat
    completion: '对话',
    text: '对话',
    image: '视觉',
    audio: '音频',
    video: '视频',
    tools: '工具调用',
    thinking: '思考',
    embed: '向量',
  }
  const capColor: Record<string, string> = {
    chat: 'bg-accent/10 text-accent',
    completion: 'bg-accent/10 text-accent',
    text: 'bg-accent/10 text-accent',
    image: 'bg-purple-500/10 text-purple-400',
    audio: 'bg-pink-500/10 text-pink-400',
    video: 'bg-pink-500/10 text-pink-400',
    tools: 'bg-emerald-500/10 text-emerald-400',
    thinking: 'bg-amber-500/10 text-amber-400',
    embed: 'bg-sky-500/10 text-sky-400',
  }

  return (
    <div className="space-y-5">
      <div>
        <SectionTitle>拉取模型</SectionTitle>
        <div className="flex gap-2">
          <input value={pullInput} onChange={(e) => onInputChange(e.target.value)}
            onKeyDown={(e) => {
              if (e.key !== 'Enter') return
              if (selectedFile) handlePull()
              else runSearch(pullInput)
            }}
            placeholder="搜索 HuggingFace GGUF 模型，例：gemma-3-4b、qwen2.5-7b"
            className="flex-1 bg-surface-secondary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent/60 transition-colors" />
          {selectedFile ? (
            <button onClick={handlePull}
              disabled={(`${selectedRepo}/${selectedFile}` in pulls) && !pulls[`${selectedRepo}/${selectedFile}`].done && !pulls[`${selectedRepo}/${selectedFile}`].error && !pulls[`${selectedRepo}/${selectedFile}`].interrupted}
              className="px-4 py-2 bg-accent text-white text-sm font-medium rounded-lg disabled:opacity-40 hover:bg-accent-hover transition-colors">
              拉取
            </button>
          ) : (
            <button onClick={() => runSearch(pullInput)}
              disabled={!pullInput.trim() || searching}
              className="px-4 py-2 bg-accent text-white text-sm font-medium rounded-lg disabled:opacity-40 hover:bg-accent-hover transition-colors">
              {searching ? '搜索中…' : '搜索'}
            </button>
          )}
        </div>
        {selectedFile && selectedRepo && (
          <p className="mt-2 text-2xs text-text-muted">
            已选择：<span className="text-text-primary font-mono">{selectedRepo}/{selectedFile}</span>
            <button
              onClick={() => { setSelectedFile(null); setSelectedRepo(null); setRepoFiles([]) }}
              className="ml-2 text-accent hover:underline">重新选择</button>
          </p>
        )}
        {searchError && (
          <p className="mt-2 text-2xs text-error">{searchError}</p>
        )}
        {pullError && (
          <p className="mt-2 text-2xs text-error">{pullError}</p>
        )}
        {hasActivePull && !pullError && (
          <p className="mt-2 text-2xs text-text-muted">下载将在后台进行，可关闭此页面继续浏览。</p>
        )}

        {/* Search results: show only before any file is picked, and only after the user actually searched */}
        {!selectedFile && searchResults.length > 0 && !selectedRepo && (
          <div className="mt-3">
            <p className="text-xs text-text-muted mb-1.5">搜索结果（点击查看可下载的 GGUF 文件）</p>
            <div className="max-h-72 overflow-y-auto space-y-1 pr-1">
              {searchResults.map((m) => (
                <button
                  key={m.id}
                  onClick={() => selectRepo(m.id)}
                  className="w-full text-left flex items-center justify-between gap-2 px-3 py-2 bg-surface-tertiary border border-border rounded-md hover:border-accent/50 hover:bg-surface-secondary transition-colors"
                >
                  <span className="text-xs text-text-primary truncate font-mono">{m.id}</span>
                  <span className="text-2xs text-text-muted shrink-0">
                    {typeof m.downloads === 'number' ? `↓ ${m.downloads.toLocaleString()}` : ''}
                    {typeof m.likes === 'number' ? `  ♥ ${m.likes}` : ''}
                  </span>
                </button>
              ))}
            </div>
          </div>
        )}

        {/* Repo files panel */}
        {selectedRepo && !selectedFile && (
          <div className="mt-3">
            <div className="flex items-center justify-between mb-1.5">
              <p className="text-xs text-text-muted">
                <span className="font-mono text-text-secondary">{selectedRepo}</span> 下的 GGUF 文件
              </p>
              <button
                onClick={() => { setSelectedRepo(null); setRepoFiles([]) }}
                className="text-2xs text-accent hover:underline">返回搜索结果</button>
            </div>
            {loadingFiles ? (
              <p className="text-xs text-text-muted">加载中…</p>
            ) : repoFiles.length === 0 ? (
              <p className="text-xs text-text-muted">该仓库未找到 GGUF 文件</p>
            ) : (
              <div className="max-h-64 overflow-y-auto space-y-1 pr-1">
                {repoFiles.map((f) => (
                  <button
                    key={f.path}
                    onClick={() => setSelectedFile(f.path)}
                    className="w-full text-left flex items-center justify-between gap-2 px-3 py-2 bg-surface-tertiary border border-border rounded-md hover:border-accent/50 hover:bg-surface-secondary transition-colors"
                  >
                    <span className="text-xs text-text-primary truncate font-mono">{f.path}</span>
                    {f.size ? (
                      <span className="text-2xs text-text-muted shrink-0">{fmtSize(f.size)}</span>
                    ) : null}
                  </button>
                ))}
              </div>
            )}
          </div>
        )}

        {/* Recommended models — only when nothing has been searched yet */}
        {!pullInput.trim() && !selectedRepo && recommended.length > 0 && (
          <div className="mt-3">
            <p className="text-xs text-text-muted mb-1.5">推荐模型（基于当前硬件）</p>
            <div className="flex flex-wrap gap-1.5">
              {recommended.map((name) => {
                // Recommended entries are full "owner/repo/file.gguf" refs — split for one-click pull.
                const parts = name.split('/')
                const isFullRef = parts.length >= 3
                return (
                  <button
                    key={name}
                    onClick={() => {
                      if (isFullRef) {
                        const file = parts[parts.length - 1]
                        const repo = parts.slice(0, -1).join('/')
                        setSelectedRepo(repo)
                        setSelectedFile(file)
                        setPullInput(name)
                      } else {
                        // Only repo provided: select it and load files.
                        setPullInput(name)
                        selectRepo(name)
                      }
                    }}
                    className="text-xs px-2 py-1 bg-surface-tertiary border border-border rounded-md text-text-secondary hover:border-accent/50 hover:text-accent transition-colors"
                  >
                    {name}
                  </button>
                )
              })}
            </div>
          </div>
        )}

        {pullEntries.length > 0 && (
          <div className="mt-4 space-y-2">
            {pullEntries.map((p) => (
              <PullProgressCard
                key={p.model}
                entry={p}
                onDismiss={async () => {
                  dismissPull(p.model)
                  try {
                    await ws.call('models.pull_dismiss', { model: p.model })
                  } catch { /* ignore */ }
                }}
                onRetry={async () => {
                  // Re-derive repo/file: prefer stored fields, then split the model id.
                  let repo = p.repo
                  let file = p.file
                  if (!repo || !file) {
                    const parts = p.model.split('/')
                    if (parts.length >= 3) {
                      file = parts[parts.length - 1]
                      repo = parts.slice(0, -1).join('/')
                    }
                  }
                  if (!repo || !file) return
                  startPull(p.model, repo, file)
                  try {
                    await ws.call('models.pull', { repo, file })
                  } catch (e) {
                    failPull(p.model, e instanceof Error ? e.message : String(e))
                  }
                }}
              />
            ))}
          </div>
        )}
      </div>
      <div>
        <SectionTitle>已有模型</SectionTitle>
        {loading ? <p className="text-sm text-text-muted">加载中…</p>
          : models.length === 0 ? <p className="text-sm text-text-muted">暂无模型，请先拉取</p>
            : (
              <Card>
                {models.map((m, i) => (
                  <div key={i} className="flex items-center justify-between px-4 py-3 border-b border-border/40 last:border-0">
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <p className="text-sm font-medium text-text-primary truncate">{m.name}</p>
                        {m.is_remote && (
                          <span className="text-2xs px-1.5 py-0.5 bg-purple-500/15 text-purple-400 border border-purple-500/30 rounded shrink-0">远端</span>
                        )}
                      </div>
                      <div className="flex items-center gap-2 mt-1 flex-wrap">
                        {m.param_size && (
                          <span className="text-2xs px-1.5 py-0.5 bg-surface-tertiary border border-border rounded text-text-secondary font-mono">
                            {m.param_size}
                          </span>
                        )}
                        {m.family && (
                          <span className="text-2xs text-text-muted">{m.family}</span>
                        )}
                        {m.quant_level && (
                          <span className="text-2xs px-1.5 py-0.5 bg-surface-tertiary border border-border rounded text-text-muted font-mono">
                            {m.quant_level}
                          </span>
                        )}
                        {m.context_len ? (
                          <span className="text-2xs text-text-muted">ctx {m.context_len.toLocaleString()}</span>
                        ) : null}
                        {m.capabilities?.map((c) => (
                          <span key={c} className={`text-2xs px-1.5 py-0.5 rounded ${capColor[c] ?? 'bg-surface-tertiary text-text-muted border border-border'}`}>
                            {capLabel[c] ?? c}
                          </span>
                        ))}
                      </div>
                    </div>
                    <div className="flex items-center gap-2 ml-3 shrink-0">
                      {!m.is_remote && m.size_bytes ? <span className="text-2xs text-text-muted">{fmtSize(m.size_bytes)}</span> : null}
                      <Badge color={m.is_remote ? 'gray' : 'green'}>{m.is_remote ? '远端' : '已下载'}</Badge>
                      {!m.is_remote && (
                        <button
                          onClick={() => handleDelete(m.name)}
                          disabled={deleting === m.name}
                          className={`p-1 transition-colors disabled:opacity-40 ${confirmDelete === m.name ? 'text-error' : 'text-text-muted hover:text-error'}`}
                          title={confirmDelete === m.name ? '再次点击确认删除' : '删除模型'}
                        >
                          {confirmDelete === m.name ? (
                            <span className="text-2xs font-medium">确认?</span>
                          ) : (
                            <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
                              <path strokeLinecap="round" strokeLinejoin="round" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                            </svg>
                          )}
                        </button>
                      )}
                    </div>
                  </div>
                ))}
              </Card>
            )}
      </div>
    </div>
  )
}
