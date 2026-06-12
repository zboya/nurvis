import { useState, useRef, useEffect } from 'react'
import { getWs } from '../../lib/ws'
import type { LibraryModel, RepoFile } from '../settings/types'
import { Spinner, Button } from '../ui'

interface Props {
  open: boolean
  onClose: () => void
  /** Called when user picks a concrete file. The model is the full "owner/repo/file.gguf" ref. */
  onPick: (model: string) => void
}

export function ModelSearchDialog({ open, onClose, onPick }: Props) {
  const [pullInput, setPullInput] = useState('')
  const [searchResults, setSearchResults] = useState<LibraryModel[]>([])
  const [searching, setSearching] = useState(false)
  const [searchError, setSearchError] = useState<string | null>(null)
  const [selectedRepo, setSelectedRepo] = useState<string | null>(null)
  const [repoFiles, setRepoFiles] = useState<RepoFile[]>([])
  const [loadingFiles, setLoadingFiles] = useState(false)
  const searchTimer = useRef<number | null>(null)
  const searchSeq = useRef(0)

  useEffect(() => {
    if (!open) {
      // Reset on close
      setPullInput('')
      setSearchResults([])
      setSelectedRepo(null)
      setRepoFiles([])
      setSearchError(null)
      setSearching(false)
    }
  }, [open])

  // Close on ESC
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

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
      const r = await getWs().call<{ library: LibraryModel[] }>('models.library', { Search: q, Limit: 20 })
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

  const onInputChange = (val: string) => {
    setPullInput(val)
    if (selectedRepo) {
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
    setRepoFiles([])
    setSearchError(null)
    setLoadingFiles(true)
    try {
      const r = await getWs().call<{ files: RepoFile[] }>('models.repo_files', { repo })
      setRepoFiles(r.files ?? [])
    } catch (e) {
      setSearchError(e instanceof Error ? e.message : String(e))
    } finally {
      setLoadingFiles(false)
    }
  }

  const fmtSize = (b?: number) => {
    if (!b) return ''
    if (b > 1e9) return `${(b / 1e9).toFixed(1)} GB`
    return `${(b / 1e6).toFixed(0)} MB`
  }

  const pickFile = (file: string) => {
    if (!selectedRepo) return
    onPick(`${selectedRepo}/${file}`)
    onClose()
  }

  if (!open) return null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm animate-fade-in"
      onClick={onClose}>
      <div
        className="w-full max-w-xl mx-4 bg-surface-secondary border border-border rounded-2xl shadow-2xl overflow-hidden"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-3.5 border-b border-border">
          <div>
            <h3 className="text-sm font-semibold text-text-primary">搜索模型</h3>
            <p className="text-2xs text-text-muted mt-0.5">从 HuggingFace 搜索 GGUF 模型并选择文件下载</p>
          </div>
          <button
            onClick={onClose}
            className="p-1 text-text-muted hover:text-text-primary transition-colors"
            title="关闭"
          >
            <svg className="w-4 h-4" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        {/* Body */}
        <div className="p-5 space-y-3">
          <div className="flex gap-2">
            <input
              autoFocus
              value={pullInput}
              onChange={(e) => onInputChange(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Enter') runSearch(pullInput) }}
              placeholder="搜索 HuggingFace GGUF 模型，例：gemma-3-4b、qwen2.5-7b"
              className="flex-1 bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent/60 transition-colors"
            />
            <Button variant="primary" size="md" onClick={() => runSearch(pullInput)} disabled={!pullInput.trim() || searching}>
              {searching ? '搜索中…' : '搜索'}
            </Button>
          </div>

          {searchError && <p className="text-2xs text-error">{searchError}</p>}

          {/* Search results */}
          {!selectedRepo && searchResults.length > 0 && (
            <div>
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

          {!selectedRepo && !searching && pullInput.trim() && searchResults.length === 0 && !searchError && (
            <p className="text-xs text-text-muted">未找到结果，换个关键词试试</p>
          )}

          {/* Repo files */}
          {selectedRepo && (
            <div>
              <div className="flex items-center justify-between mb-1.5">
                <p className="text-xs text-text-muted">
                  <span className="font-mono text-text-secondary">{selectedRepo}</span> 下的 GGUF 文件
                </p>
                <button
                  onClick={() => { setSelectedRepo(null); setRepoFiles([]) }}
                  className="text-2xs text-accent hover:underline">返回搜索结果</button>
              </div>
              {loadingFiles ? (
                <div className="flex items-center gap-2 py-3"><Spinner size="sm" /><span className="text-xs text-text-muted">加载中…</span></div>
              ) : repoFiles.length === 0 ? (
                <p className="text-xs text-text-muted">该仓库未找到 GGUF 文件</p>
              ) : (
                <div className="max-h-72 overflow-y-auto space-y-1 pr-1">
                  {repoFiles.map((f) => (
                    <button
                      key={f.path}
                      onClick={() => pickFile(f.path)}
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

          {/* Empty state */}
          {!selectedRepo && !pullInput.trim() && (
            <p className="text-xs text-text-muted py-2">输入关键词开始搜索 HuggingFace 上的 GGUF 模型</p>
          )}
        </div>
      </div>
    </div>
  )
}
