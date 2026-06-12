import { useState, useEffect, useRef } from 'react'
import { getWs } from '../../lib/ws'
import type { ModelRecommend, RuntimeStatus, PullProgress } from '../../types'
import { Spinner, Button } from '../ui'
import { ModelSearchDialog } from './ModelSearchDialog'
import { useModelStore } from '../../stores/model-store'

type Phase = 'detecting' | 'recommend' | 'pulling' | 'running' | 'done' | 'error'

interface Props {
  onComplete: (model: string) => void
}

export function SetupStep({ onComplete }: Props) {
  const [phase, setPhase] = useState<Phase>('detecting')
  const [status, setStatus] = useState<RuntimeStatus | null>(null)
  const [recommend, setRecommend] = useState<ModelRecommend | null>(null)
  const [selectedModel, setSelectedModel] = useState<string>('')
  const [progress, setProgress] = useState<PullProgress | null>(null)
  const [error, setError] = useState('')
  const [searchOpen, setSearchOpen] = useState(false)
  const detectedRef = useRef(false)
  const selectedModelRef = useRef('')

  // Pulls hydrated from backend `models.pull_list` (subscribed at App root).
  // We only care about the "interrupted / failed" rows here so the user can
  // resume or dismiss them right from the onboarding screen.
  const pulls = useModelStore((s) => s.pulls)
  const startPull = useModelStore((s) => s.start)
  const failPull = useModelStore((s) => s.fail)
  const dismissPull = useModelStore((s) => s.dismiss)
  const stalledPulls = Object.values(pulls)
    .filter((p) => (p.interrupted || (p.error && !p.done)) && !!p.model)
    .sort((a, b) => (b.finishedAt ?? b.startedAt) - (a.finishedAt ?? a.startedAt))

  // Subscribe to events: StrictMode causes mount→unmount→mount; symmetric on/unsub is safe
  useEffect(() => {
    const ws = getWs()
    const unsub = ws.on('models.pull.progress', (raw) => {
      console.log('[WS] models.pull.progress', raw)
      const p = raw as PullProgress
      if (selectedModelRef.current && p.model && p.model !== selectedModelRef.current) return
      setPhase((prev) => (prev === 'recommend' || prev === 'pulling' ? 'pulling' : prev))
      setProgress(p)
      if (p.status === 'success') {
        setPhase('running')
        runModel(selectedModelRef.current)
      }
    })
    return unsub
  }, [])

  // Trigger hardware/model detection only once
  useEffect(() => {
    if (detectedRef.current) return
    detectedRef.current = true
    detect()
  }, [])

  async function detect() {
    setPhase('detecting')
    setError('')
    try {
      const ws = getWs()
      const [runtimeStatus, rec] = await Promise.all([
        ws.call<RuntimeStatus>('runtime.status'),
        ws.call<ModelRecommend>('models.recommend'),
      ])
      setStatus(runtimeStatus)
      setRecommend(rec)
      setSelectedModel(rec.default_model ?? rec.recommended?.[0] ?? 'ggml-org/gemma-3-1b-it-GGUF/gemma-3-1b-it-Q4_K_M.gguf')
      setPhase('recommend')
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      setPhase('error')
    }
  }

  async function pullAndContinue() {
    if (!selectedModel) return
    setPhase('pulling')
    setProgress(null)
    selectedModelRef.current = selectedModel
    try {
      // models.pull is async; progress and completion are pushed via models.pull.progress events
      await getWs().call('models.pull', { model: selectedModel })
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      setPhase('error')
    }
  }

  // Resume an interrupted/failed pull. Switches the page into the live "pulling"
  // phase so the existing progress UI takes over from here.
  async function resumePull(model: string, repo?: string, file?: string) {
    let r = repo
    let f = file
    if (!r || !f) {
      const parts = model.split('/')
      if (parts.length >= 3) {
        f = parts[parts.length - 1]
        r = parts.slice(0, -1).join('/')
      }
    }
    setSelectedModel(model)
    selectedModelRef.current = model
    setProgress(null)
    setPhase('pulling')
    startPull(model, r, f)
    try {
      await getWs().call('models.pull', r && f ? { repo: r, file: f } : { model })
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      failPull(model, msg)
      setError(msg)
      setPhase('error')
    }
  }

  // Dismiss a stalled row both locally and on the backend (best effort).
  async function dismissStalled(model: string) {
    dismissPull(model)
    try {
      await getWs().call('models.pull_dismiss', { model })
    } catch { /* ignore */ }
  }

  async function skipPull() {
    onComplete(selectedModel)
  }

  async function runModel(model: string) {
    try {
      await getWs().call('models.run', { model })
      setPhase('done')
      setTimeout(() => onComplete(model), 800)
    } catch (e) {
      // Inference startup failure doesn't block the flow; degrade and proceed
      console.warn('[SetupStep] models.run failed, proceeding anyway', e)
      setPhase('done')
      setTimeout(() => onComplete(model), 800)
    }
  }

  const pct = Math.round(progress?.percent ?? 0)

  // Map backend English status to localized display text
  function translateStatus(s?: string): string {
    if (!s) return '连接中…'
    const lower = s.toLowerCase()
    if (lower.includes('resolving')) return '解析模型地址…'
    if (lower.includes('downloading')) return '下载中'
    if (lower.includes('pulling manifest')) return '获取模型清单…'
    if (lower.includes('pulling model') || lower.includes('pulling')) return '下载中'
    if (lower.includes('verifying')) return '校验完整性…'
    if (lower.includes('writing manifest')) return '写入清单…'
    if (lower.includes('removing')) return '清理临时文件…'
    if (lower.includes('success')) return '下载完成'
    return s
  }

  function fmtBytes(n: number): string {
    if (!n || n <= 0) return '0 B'
    if (n >= 1e9) return `${(n / 1e9).toFixed(2)} GB`
    if (n >= 1e6) return `${(n / 1e6).toFixed(1)} MB`
    if (n >= 1e3) return `${(n / 1e3).toFixed(0)} KB`
    return `${n} B`
  }

  return (
    <div className="space-y-5 animate-slide-up">
      {/* Hardware info */}
      {recommend && (
        <div className="bg-surface-tertiary/50 border border-border rounded-xl p-4 text-xs text-text-muted space-y-1">
          <div className="flex items-center gap-2">
            <span className="text-text-secondary font-medium">硬件</span>
            <span>{recommend.hardware.ram_gb.toFixed(1)} GB RAM</span>
            {recommend.hardware.cpu_cores > 0 && (
              <span>{recommend.hardware.cpu_cores} 核 CPU</span>
            )}
            {recommend.hardware.is_apple_silicon && (
              <span className="px-1.5 py-0.5 bg-accent/15 text-accent rounded-md">Apple Silicon</span>
            )}
            {recommend.hardware.gpus?.filter((g) => g.name !== 'Apple Silicon').map((g) => (
              <span key={g.name} className="px-1.5 py-0.5 bg-surface-secondary border border-border rounded-md">
                {g.name}{g.vram_gb > 0 ? ` ${g.vram_gb.toFixed(0)}GB VRAM` : ''}
              </span>
            ))}
          </div>
          <div className="flex items-center gap-2 mt-1">
            <span className="text-text-secondary font-medium">Runtime</span>
            {status?.ready ? (
              <span className="text-success flex items-center gap-1">
                <span className="w-1.5 h-1.5 rounded-full bg-success inline-block" />
                已就绪
              </span>
            ) : (
              <span className="text-warning">未就绪 · 将在首次下载时初始化</span>
            )}
            {status?.lib_path && (
              <span className="text-xs text-text-muted ml-1 truncate max-w-[20em]" title={status.lib_path}>
                {status.lib_path}
              </span>
            )}
          </div>
        </div>
      )}

      {/* Interrupted / failed downloads from previous sessions */}
      {phase === 'recommend' && stalledPulls.length > 0 && (
        <div className="space-y-2">
          <div className="flex items-center justify-between">
            <p className="text-sm text-text-secondary">
              <span className="text-warning">中断的下载</span>
              <span className="ml-1.5 text-2xs text-text-muted">应用重启或异常退出后保留的下载任务</span>
            </p>
          </div>
          <div className="space-y-1.5">
            {stalledPulls.map((p) => {
              const pctVal = Math.max(0, Math.min(100, Math.round(p.percent ?? 0)))
              const isInterrupted = !!p.interrupted
              return (
                <div
                  key={p.model}
                  className={[
                    'rounded-xl border px-3.5 py-2.5',
                    isInterrupted ? 'border-amber-500/40 bg-amber-500/5' : 'border-error/40 bg-error/5',
                  ].join(' ')}
                >
                  <div className="flex items-center justify-between gap-3">
                    <div className="flex items-center gap-2.5 min-w-0">
                      <div className={[
                        'w-6 h-6 rounded-full flex items-center justify-center shrink-0',
                        isInterrupted ? 'bg-amber-500/15' : 'bg-error/15',
                      ].join(' ')}>
                        {isInterrupted ? (
                          <svg className="w-3.5 h-3.5 text-amber-500" fill="none" stroke="currentColor" strokeWidth={2.5} viewBox="0 0 24 24">
                            <path strokeLinecap="round" strokeLinejoin="round" d="M10 9v6m4-6v6M5 5h14a2 2 0 012 2v10a2 2 0 01-2 2H5a2 2 0 01-2-2V7a2 2 0 012-2z" />
                          </svg>
                        ) : (
                          <svg className="w-3.5 h-3.5 text-error" fill="none" stroke="currentColor" strokeWidth={2.5} viewBox="0 0 24 24">
                            <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
                          </svg>
                        )}
                      </div>
                      <div className="min-w-0">
                        <p className="text-xs font-medium text-text-primary truncate font-mono" title={p.model}>{p.model}</p>
                        <p className={[
                          'text-2xs mt-0.5 truncate',
                          isInterrupted ? 'text-amber-500' : 'text-error',
                        ].join(' ')}>
                          {p.error || (isInterrupted ? '已中断（应用重启），可点继续下载' : '下载失败')}
                          {pctVal > 0 ? ` · 进度 ${pctVal}%` : ''}
                          {p.total > 0 ? ` · ${fmtBytes(p.current)} / ${fmtBytes(p.total)}` : ''}
                        </p>
                      </div>
                    </div>
                    <div className="flex items-center gap-1.5 shrink-0">
                      <button
                        onClick={() => resumePull(p.model, p.repo, p.file)}
                        className="text-2xs px-2 py-1 bg-accent/10 hover:bg-accent/20 text-accent rounded-md transition-colors"
                      >
                        继续下载
                      </button>
                      <button
                        onClick={() => dismissStalled(p.model)}
                        className="text-text-muted hover:text-text-primary p-1 transition-colors"
                        title="忽略"
                      >
                        <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
                          <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
                        </svg>
                      </button>
                    </div>
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      )}

      {/* Model selection */}
      {phase === 'recommend' && recommend && (
        <div className="space-y-3">
          <p className="text-sm text-text-secondary">根据你的硬件，推荐以下模型。选择一个下载以开始使用：</p>
          <div className="space-y-2">
            {recommend.recommended.map((m, i) => (
              <button
                key={m}
                onClick={() => setSelectedModel(m)}
                className={[
                  'w-full flex items-center justify-between p-3 rounded-xl border transition-all text-left',
                  selectedModel === m
                    ? 'border-accent bg-accent/10'
                    : 'border-border bg-surface-tertiary/30 hover:border-accent/40',
                ].join(' ')}
              >
                <div className="flex items-center gap-3">
                  <div className={[
                    'w-4 h-4 rounded-full border-2 flex items-center justify-center shrink-0',
                    selectedModel === m ? 'border-accent' : 'border-border',
                  ].join(' ')}>
                    {selectedModel === m && <div className="w-2 h-2 rounded-full bg-accent" />}
                  </div>
                  <div>
                    <p className="text-sm font-medium text-text-primary">{m}</p>
                    {i === 0 && (
                      <p className="text-xs text-text-muted mt-0.5">推荐 · 最适合当前硬件</p>
                    )}
                  </div>
                </div>
                {i === 0 && (
                  <span className="text-xs px-2 py-0.5 bg-accent/15 text-accent rounded-full">默认</span>
                )}
              </button>
            ))}
          </div>

          <div className="flex items-center justify-between pt-1 gap-3">
            <button
              onClick={skipPull}
              className="text-xs text-text-muted hover:text-text-secondary transition-colors shrink-0"
            >
              跳过，稍后再下载
            </button>
            <div className="flex items-center gap-2">
              <Button variant="secondary" size="md" onClick={() => setSearchOpen(true)}>
                <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" d="M21 21l-4.35-4.35M11 19a8 8 0 100-16 8 8 0 000 16z" />
                </svg>
                搜索模型
              </Button>
              <Button variant="primary" size="md" onClick={pullAndContinue} disabled={!selectedModel}>
                下载
              </Button>
            </div>
          </div>
        </div>
      )}

      <ModelSearchDialog
        open={searchOpen}
        onClose={() => setSearchOpen(false)}
        onPick={(model) => {
          // Append the searched model to the recommended list (if not already there) and select it.
          setRecommend((prev) => {
            if (!prev) return prev
            if (prev.recommended.includes(model)) return prev
            return { ...prev, recommended: [...prev.recommended, model] }
          })
          setSelectedModel(model)
        }}
      />

      {/* Download progress */}
      {phase === 'pulling' && (
        <div className="space-y-3">
          <div className="flex items-center justify-between gap-3">
            <div className="flex items-center gap-3 min-w-0">
              {progress ? (
                <span className="relative flex h-2.5 w-2.5 shrink-0">
                  <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-accent opacity-60" />
                  <span className="relative inline-flex h-2.5 w-2.5 rounded-full bg-accent" />
                </span>
              ) : (
                <Spinner size="sm" />
              )}
              <div className="min-w-0">
                <p className="text-sm font-medium text-text-primary truncate">
                  正在下载 {selectedModel}
                </p>
                <p className="text-xs text-text-muted mt-0.5 truncate">
                  {translateStatus(progress?.status)}
                </p>
              </div>
            </div>
            <div className="text-right shrink-0">
              <p className="text-base font-semibold text-text-primary tabular-nums">{pct}%</p>
              {progress?.total && progress.total > 0 ? (
                <p className="text-2xs text-text-muted tabular-nums mt-0.5">
                  {fmtBytes(progress.current)} / {fmtBytes(progress.total)}
                </p>
              ) : null}
            </div>
          </div>

      {/* Progress bar */}
          <div className="relative h-2 w-full overflow-hidden rounded-full bg-surface-tertiary">
            {pct > 0 ? (
              <div
                className="relative h-full rounded-full progress-gradient transition-[width] duration-500 ease-out"
                style={{ width: `${pct}%` }}
              >
                {/* Striped animation */}
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
              // Indeterminate state before progress arrives: shimmer
              <div
                className="absolute top-0 bottom-0 w-1/3 rounded-full progress-gradient opacity-70"
                style={{ left: '-35%', animation: 'progress-indeterminate 1.4s ease-in-out infinite' }}
              />
            )}
          </div>
        </div>
      )}

      {/* Start inference */}
      {phase === 'running' && (
        <div className="flex items-center gap-3 py-2">
          <Spinner size="sm" />
          <div>
            <p className="text-sm font-medium text-text-primary">正在启动模型推理…</p>
            <p className="text-xs text-text-muted mt-0.5">{selectedModel} 预热中，首次加载需要片刻</p>
          </div>
        </div>
      )}

      {/* Done */}
      {phase === 'done' && (
        <div className="flex items-center gap-3 py-2">
          <div className="w-8 h-8 rounded-full bg-success/20 flex items-center justify-center shrink-0">
            <svg className="w-4 h-4 text-success" fill="none" stroke="currentColor" strokeWidth={2.5} viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
            </svg>
          </div>
          <div>
            <p className="text-sm font-medium text-text-primary">模型已就绪！</p>
            <p className="text-xs text-text-muted">正在进入应用…</p>
          </div>
        </div>
      )}

      {/* Detecting */}
      {phase === 'detecting' && (
        <div className="flex items-center gap-3 py-4">
          <Spinner />
          <p className="text-sm text-text-muted">正在检测本地推理运行时和硬件信息…</p>
        </div>
      )}

      {/* Error */}
      {phase === 'error' && (
        <div className="space-y-3">
          <div className="bg-error/10 border border-error/30 rounded-xl p-4">
            <p className="text-sm text-error">{error || '未知错误'}</p>
          </div>
          <div className="flex gap-2">
            <Button variant="secondary" onClick={detect}>重试</Button>
            <Button variant="ghost" onClick={skipPull}>跳过</Button>
          </div>
        </div>
      )}
    </div>
  )
}