import { useEffect, useRef, useState } from 'react'
import { getWs } from '../../lib/ws'
import { useUiStore } from '../../stores/ui-store'
import type { Agent, ModelRecommend, PullProgress } from '../../types'
import { Spinner, Button } from '../ui'

interface Props {
  onComplete: () => void
}

// Each task represents one model to download. The chat-bot's model is decided
// by hardware recommendation at runtime; the image-bot's three models are
// fixed (chat LLM + VAE + diffusion model).
type TaskKey = 'chat' | 'image_chat' | 'image_vae' | 'image_diffusion'
// Which agent a task contributes to. Used to decide when 对话宝 / 生图宝 can
// be considered "ready" independently.
type Owner = 'chat_bot' | 'image_bot' | 'shared'

interface Task {
  key: TaskKey
  label: string
  owner: Owner
  // Backend ref id ("<repo>/<file>") — populated after recommendation arrives.
  model: string
  repo: string
  file: string
  // Live state
  status: 'pending' | 'downloading' | 'success' | 'error'
  percent: number
  current: number
  total: number
  error?: string
}

// Fixed model refs for the image-bot. Kept in sync with the requirement spec.
const IMAGE_BOT_CHAT_REPO = 'unsloth/Qwen3-4B-Instruct-2507-GGUF'
const IMAGE_BOT_CHAT_FILE = 'Qwen3-4B-Instruct-2507-Q4_K_M.gguf'
const IMAGE_BOT_VAE_REPO = 'ffxvs/vae-flux'
const IMAGE_BOT_VAE_FILE = 'ae.safetensors'
const IMAGE_BOT_DIFFUSION_REPO = 'leejet/Z-Image-Turbo-GGUF'
const IMAGE_BOT_DIFFUSION_FILE = 'z_image_turbo-Q6_K.gguf'

const CHAT_BOT_PRESET = {
  emoji: '🤖',
  name: '对话宝',
  role: '通用助手',
  prompt: '你是一个聪明、友好、乐于助人的AI助手。',
}
const IMAGE_BOT_PRESET = {
  emoji: '🎨',
  name: '生图宝',
  role: '图像创作助手',
  prompt: '你是一位富有创造力的图像创作助手，根据用户描述生成图片。',
}

function fmtBytes(n: number): string {
  if (!n || n <= 0) return '0 B'
  if (n >= 1e9) return `${(n / 1e9).toFixed(2)} GB`
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)} MB`
  if (n >= 1e3) return `${(n / 1e3).toFixed(0)} KB`
  return `${n} B`
}

function fmtSpeed(bytesPerSec: number): string {
  if (!bytesPerSec || bytesPerSec <= 0) return '—'
  return `${fmtBytes(bytesPerSec)}/s`
}

function fmtDuration(sec: number): string {
  if (!isFinite(sec) || sec <= 0) return '—'
  if (sec < 60) return `${Math.ceil(sec)} 秒`
  if (sec < 3600) {
    const m = Math.floor(sec / 60)
    const s = Math.ceil(sec - m * 60)
    return s > 0 ? `${m} 分 ${s} 秒` : `${m} 分钟`
  }
  const h = Math.floor(sec / 3600)
  const m = Math.ceil((sec - h * 3600) / 60)
  return m > 0 ? `${h} 小时 ${m} 分` : `${h} 小时`
}

export function PreparingStep({ onComplete }: Props) {
  const [phase, setPhase] = useState<'detecting' | 'downloading' | 'finalizing' | 'done' | 'error'>('detecting')
  const [tasks, setTasks] = useState<Task[]>([])
  const [error, setError] = useState('')
  // True once 对话宝 agent has been created on the backend. Required before
  // we can offer the "先用对话宝聊起来" early-exit button.
  const [chatBotAgentId, setChatBotAgentId] = useState<string | null>(null)
  const setActiveAgent = useUiStore((s) => s.setActiveAgent)

  // EMA-smoothed download speed in bytes/sec, plus the most recent total
  // remaining bytes — used to render the ETA hint.
  const [speedBps, setSpeedBps] = useState(0)
  const [remainingBytes, setRemainingBytes] = useState(0)

  // Per-task list is collapsed by default to keep the prepare page compact;
  // the user can expand it to inspect each model's progress.
  const [tasksExpanded, setTasksExpanded] = useState(false)

  const startedRef = useRef(false)
  const tasksRef = useRef<Task[]>([])
  // Sampling state for speed estimation.
  const lastSampleRef = useRef<{ ts: number; bytes: number } | null>(null)
  // Keep a ref mirror so the WS event handler always sees the latest tasks
  // without re-subscribing.
  useEffect(() => { tasksRef.current = tasks }, [tasks])

  // Subscribe to download progress once.
  useEffect(() => {
    const ws = getWs()
    const unsub = ws.on('models.pull.progress', (raw) => {
      const p = raw as PullProgress
      if (!p?.model) return
      setTasks((prev) => {
        const idx = prev.findIndex((t) => t.model === p.model)
        if (idx < 0) return prev
        const next = prev.slice()
        const cur = next[idx]
        const isError = p.status === 'error' || !!p.error
        const isSuccess = p.status === 'success' || (p.percent >= 100 && !isError)
        next[idx] = {
          ...cur,
          status: isError ? 'error' : isSuccess ? 'success' : 'downloading',
          percent: Math.max(cur.percent, p.percent ?? 0),
          current: p.current ?? cur.current,
          total: p.total ?? cur.total,
          error: isError ? (p.error || '下载失败') : undefined,
        }
        return next
      })
    })
    return unsub
  }, [])

  // Speed / ETA sampling loop. Runs every ~1s while there are active downloads.
  // Uses exponential moving average to keep the displayed value stable.
  useEffect(() => {
    const timer = setInterval(() => {
      const list = tasksRef.current
      if (list.length === 0) return
      const totalDone = list.reduce((s, t) => s + (t.current || 0), 0)
      const totalSize = list.reduce((s, t) => s + (t.total || 0), 0)
      const remaining = Math.max(0, totalSize - totalDone)
      setRemainingBytes(totalSize > 0 ? remaining : 0)

      const now = Date.now()
      const last = lastSampleRef.current
      lastSampleRef.current = { ts: now, bytes: totalDone }
      if (!last) return
      const dt = (now - last.ts) / 1000
      if (dt <= 0) return
      const instant = Math.max(0, (totalDone - last.bytes) / dt)
      // EMA: alpha=0.3 — react fast but smooth out noise from chunked writes.
      setSpeedBps((prev) => (prev <= 0 ? instant : prev * 0.7 + instant * 0.3))
    }, 1000)
    return () => clearInterval(timer)
  }, [])

  // Drive the full flow once.
  useEffect(() => {
    if (startedRef.current) return
    startedRef.current = true
    void run()
  }, [])

  // Watch chat-bot task: once its model is fully downloaded, create 对话宝
  // (idempotency handled inside ensureChatBotAgent) so the early-exit button
  // can light up. This runs independently of the rest of the flow.
  useEffect(() => {
    if (chatBotAgentId) return
    const chatTask = tasks.find((t) => t.owner === 'chat_bot' || t.owner === 'shared')
    if (!chatTask || chatTask.status !== 'success') return
    let cancelled = false
    ;(async () => {
      const id = await ensureChatBotAgent(chatTask.model)
      if (!cancelled && id) setChatBotAgentId(id)
    })()
    return () => { cancelled = true }
  }, [tasks, chatBotAgentId])

  async function run() {
    try {
      setPhase('detecting')
      // 1. Ensure runtime libs are downloaded (idempotent).
      try { await getWs().call('runtime.ensure') } catch { /* non-fatal */ }

      // 2. Get hardware-recommended chat model for "对话宝".
      const rec = await getWs().call<ModelRecommend>('models.recommend')
      const chatRef =
        rec.default_model ||
        rec.recommended?.[0] ||
        'ggml-org/gemma-3-1b-it-GGUF/gemma-3-1b-it-Q4_K_M.gguf'
      const { repo: chatRepo, file: chatFile } = splitRef(chatRef)

      // Build task list. Note: image-bot's chat model may coincide with
      // 对话宝's recommendation — in that case we deduplicate and mark
      // owner = 'shared' so it counts toward both bots.
      const imageChatRef = `${IMAGE_BOT_CHAT_REPO}/${IMAGE_BOT_CHAT_FILE}`
      const imageVaeRef = `${IMAGE_BOT_VAE_REPO}/${IMAGE_BOT_VAE_FILE}`
      const imageDiffRef = `${IMAGE_BOT_DIFFUSION_REPO}/${IMAGE_BOT_DIFFUSION_FILE}`

      const built: Task[] = [
        mkTask('chat', '对话宝 · 对话模型', 'chat_bot', chatRef, chatRepo, chatFile),
        mkTask('image_chat', '生图宝 · 对话模型', 'image_bot', imageChatRef, IMAGE_BOT_CHAT_REPO, IMAGE_BOT_CHAT_FILE),
        mkTask('image_vae', '生图宝 · VAE', 'image_bot', imageVaeRef, IMAGE_BOT_VAE_REPO, IMAGE_BOT_VAE_FILE),
        mkTask('image_diffusion', '生图宝 · 扩散模型', 'image_bot', imageDiffRef, IMAGE_BOT_DIFFUSION_REPO, IMAGE_BOT_DIFFUSION_FILE),
      ]
      const seen = new Set<string>()
      const deduped: Task[] = []
      for (const t of built) {
        const existing = deduped.find((x) => x.model === t.model)
        if (existing) {
          // Mark the existing task as shared so both bots wait on it.
          existing.owner = 'shared'
          continue
        }
        seen.add(t.model)
        deduped.push(t)
      }
      setTasks(deduped)
      setPhase('downloading')

      // 3. Kick off all downloads in parallel. The backend already short-circuits
      //    when the file is already present locally, so this is safe on re-runs.
      await Promise.all(
        deduped.map((t) =>
          getWs()
            .call('models.pull', { repo: t.repo, file: t.file })
            .catch((e) => {
              const msg = e instanceof Error ? e.message : String(e)
              setTasks((prev) => {
                const idx = prev.findIndex((x) => x.model === t.model)
                if (idx < 0) return prev
                const next = prev.slice()
                next[idx] = { ...next[idx], status: 'error', error: msg }
                return next
              })
            }),
        ),
      )

      // 4. Wait until all tasks reach terminal state (success / error).
      await waitAllDone(tasksRef)

      // If anything failed, surface error and let the user retry.
      const failed = tasksRef.current.filter((t) => t.status === 'error')
      if (failed.length > 0) {
        setError(`部分模型下载失败：${failed.map((f) => f.label).join('、')}`)
        setPhase('error')
        return
      }

      // 5. Create remaining default agents (对话宝 may already exist from the
      //    early hook above). Then transition to done.
      setPhase('finalizing')
      const chatId = await ensureChatBotAgent(chatRef)
      if (chatId) setChatBotAgentId(chatId)
      await ensureImageBotAgent(imageChatRef, imageVaeRef, imageDiffRef)

      setPhase('done')
      setTimeout(() => onComplete(), 800)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      setPhase('error')
    }
  }

  // Called when the user clicks "先用对话宝聊起来". Set the active agent so the
  // chat view lands on it, then complete onboarding. The remaining downloads
  // (image-bot models) continue in the background — `models.pull` is detached
  // from the WS connection on the server side. We also fire-and-forget the
  // image-bot agent creation; it runs once its three models all finish.
  async function startWithChatBotEarly() {
    if (!chatBotAgentId) return
    setActiveAgent(chatBotAgentId)
    scheduleImageBotCreationInBackground()
    onComplete()
  }

  // Spawn an async waiter that watches tasksRef (which keeps updating from the
  // global WS event subscription registered in App root via useModelSubscription)
  // and creates 生图宝 once all its models are present. Even though this
  // component will be unmounted after onComplete(), the WS event subscription
  // we set up earlier will be torn down too — so we instead poll `models.list`
  // to detect completion. Kept minimal: idempotent agents.create-by-name.
  function scheduleImageBotCreationInBackground() {
    const imageChatRef = `${IMAGE_BOT_CHAT_REPO}/${IMAGE_BOT_CHAT_FILE}`
    const imageVaeRef = `${IMAGE_BOT_VAE_REPO}/${IMAGE_BOT_VAE_FILE}`
    const imageDiffRef = `${IMAGE_BOT_DIFFUSION_REPO}/${IMAGE_BOT_DIFFUSION_FILE}`
    const needed = [imageChatRef, imageVaeRef, imageDiffRef]
    ;(async () => {
      const deadline = Date.now() + 4 * 60 * 60 * 1000 // 4h ceiling
      while (Date.now() < deadline) {
        try {
          const res = await getWs().call<{ models?: Array<{ name: string }> }>('models.list')
          const have = new Set((res.models ?? []).map((m) => m.name))
          if (needed.every((n) => have.has(n))) {
            await ensureImageBotAgent(imageChatRef, imageVaeRef, imageDiffRef)
            return
          }
        } catch { /* keep polling */ }
        await new Promise((r) => setTimeout(r, 5000))
      }
    })()
  }

  async function retry() {
    setError('')
    startedRef.current = false
    setTasks([])
    await new Promise((r) => setTimeout(r, 0))
    startedRef.current = true
    void run()
  }

  const total = tasks.length
  const doneCount = tasks.filter((t) => t.status === 'success').length
  const overallPct = total === 0 ? 0 : Math.round(
    tasks.reduce((s, t) => s + (t.status === 'success' ? 100 : t.percent || 0), 0) / total,
  )

  // ETA — only meaningful while still downloading and we have some speed signal.
  const etaSeconds = speedBps > 0 && remainingBytes > 0 ? remainingBytes / speedBps : 0
  const chatBotReady = !!chatBotAgentId
  const imageBotPending = tasks.some((t) => t.owner !== 'chat_bot' && t.status !== 'success')

  return (
    <div className="space-y-5 animate-slide-up">
      {/* Friendly hint */}
      <div className="bg-accent/5 border border-accent/20 rounded-xl p-4">
        <p className="text-sm text-text-primary font-medium">正在为你准备 AI 助手</p>
        <p className="text-xs text-text-muted mt-1 leading-relaxed">
          我们会自动下载「对话宝」和「生图宝」运行所需的模型（约数 GB），
          下载时间取决于你的网络。请保持应用打开，下载支持断点续传，期间无需操作。
        </p>
      </div>

      {phase === 'detecting' && (
        <div className="flex items-center gap-3 py-4">
          <Spinner />
          <p className="text-sm text-text-muted">正在检测硬件并匹配推荐模型…</p>
        </div>
      )}

      {(phase === 'downloading' || phase === 'finalizing' || phase === 'done') && tasks.length > 0 && (
        <div className="space-y-3">
          {/* Overall progress header */}
          <div className="flex items-center justify-between text-xs">
            <span className="text-text-secondary">整体进度</span>
            <span className="tabular-nums text-text-secondary">
              {doneCount} / {total} 已完成 · {overallPct}%
            </span>
          </div>
          <div className="relative h-2 w-full overflow-hidden rounded-full bg-surface-tertiary">
            <div
              className="h-full rounded-full progress-gradient transition-[width] duration-500 ease-out"
              style={{ width: `${overallPct}%` }}
            />
          </div>

          {/* Speed + ETA line. Hidden once everything is done. */}
          {phase === 'downloading' && (
            <div className="flex items-center justify-between text-2xs text-text-muted tabular-nums">
              <span>当前速度：{fmtSpeed(speedBps)}</span>
              <span>
                {etaSeconds > 0 ? `预计剩余 ${fmtDuration(etaSeconds)}` : '正在估算剩余时间…'}
              </span>
            </div>
          )}

          {/* Early-exit CTA: appears as soon as 对话宝 is usable. */}
          {chatBotReady && imageBotPending && phase === 'downloading' && (
            <div className="flex items-center justify-between gap-3 rounded-xl border border-accent/30 bg-accent/10 px-3.5 py-3">
              <div className="min-w-0">
                <p className="text-xs font-medium text-text-primary">对话宝已就绪</p>
                <p className="text-2xs text-text-muted mt-0.5">
                  生图宝的模型仍在后台下载，你可以先开始聊天，完成后生图宝会自动启用
                </p>
              </div>
              <Button variant="primary" size="md" onClick={startWithChatBotEarly}>
                先用对话宝聊起来 →
              </Button>
            </div>
          )}

          {/* Per-task list — collapsed by default to keep the page tidy. */}
          <div className="pt-1">
            <button
              type="button"
              onClick={() => setTasksExpanded((v) => !v)}
              className="w-full flex items-center justify-between gap-2 px-3 py-2 rounded-lg border border-border bg-surface-tertiary/30 hover:bg-surface-tertiary/60 transition-colors text-left"
            >
              <span className="flex items-center gap-2 text-xs font-medium text-text-secondary">
                <svg
                  className={['w-3.5 h-3.5 transition-transform', tasksExpanded ? 'rotate-90' : ''].join(' ')}
                  fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24"
                >
                  <path strokeLinecap="round" strokeLinejoin="round" d="M9 5l7 7-7 7" />
                </svg>
                下载详情
                <span className="text-text-muted font-normal">（{doneCount} / {total} 已完成）</span>
              </span>
              <span className="text-2xs text-text-muted">{tasksExpanded ? '收起' : '展开'}</span>
            </button>
            {tasksExpanded && (
              <div className="space-y-2 mt-2 animate-slide-up">
                {tasks.map((t) => (
                  <TaskRow key={t.key} task={t} />
                ))}
              </div>
            )}
          </div>

          {phase === 'finalizing' && (
            <div className="flex items-center gap-2 pt-2">
              <Spinner size="sm" />
              <p className="text-xs text-text-muted">模型下载完成，正在创建默认助手…</p>
            </div>
          )}
          {phase === 'done' && (
            <div className="flex items-center gap-3 pt-2">
              <div className="w-7 h-7 rounded-full bg-success/20 flex items-center justify-center shrink-0">
                <svg className="w-4 h-4 text-success" fill="none" stroke="currentColor" strokeWidth={2.5} viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
                </svg>
              </div>
              <p className="text-sm font-medium text-text-primary">全部就绪，正在进入应用…</p>
            </div>
          )}
        </div>
      )}

      {phase === 'error' && (
        <div className="space-y-3">
          <div className="bg-error/10 border border-error/30 rounded-xl p-4 space-y-1">
            <p className="text-sm text-error">{error || '初始化失败'}</p>
            <p className="text-2xs text-text-muted">你可以重试，已下载完成的部分会自动跳过。</p>
          </div>
          <div className="flex gap-2">
            <Button variant="primary" onClick={retry}>重试</Button>
          </div>
        </div>
      )}

      {/* Bottom intro cards — describe what the user will get once setup ends. */}
      {phase !== 'error' && (
        <div className="pt-2">
          <p className="text-xs font-medium text-text-secondary mb-2">下载完成后，你将拥有</p>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-2.5">
            <BotIntroCard
              emoji={CHAT_BOT_PRESET.emoji}
              name={CHAT_BOT_PRESET.name}
              tagline="本地对话助手"
              bullets={[
                '日常问答、写作、翻译、代码解释',
                '本地运行，对话数据不离开你的电脑',
                '已根据你的硬件挑选最合适的模型',
              ]}
              ready={chatBotReady}
            />
            <BotIntroCard
              emoji={IMAGE_BOT_PRESET.emoji}
              name={IMAGE_BOT_PRESET.name}
              tagline="本地图像创作助手"
              bullets={[
                '用一句话描述就能生成图片',
                '基于 Z-Image-Turbo 扩散模型 + Flux VAE',
                '由本地对话模型负责理解和改写你的描述',
              ]}
              ready={!imageBotPending && tasks.length > 0}
            />
          </div>
        </div>
      )}
    </div>
  )
}

function TaskRow({ task }: { task: Task }) {
  const pct = Math.max(0, Math.min(100, Math.round(task.percent || 0)))
  const isDone = task.status === 'success'
  const isError = task.status === 'error'
  const isActive = task.status === 'downloading'
  return (
    <div className="rounded-xl border border-border bg-surface-tertiary/30 px-3.5 py-2.5">
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-2.5 min-w-0">
          <div className={[
            'w-6 h-6 rounded-full flex items-center justify-center shrink-0',
            isDone ? 'bg-success/20' : isError ? 'bg-error/15' : 'bg-accent/15',
          ].join(' ')}>
            {isDone ? (
              <svg className="w-3.5 h-3.5 text-success" fill="none" stroke="currentColor" strokeWidth={2.5} viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
              </svg>
            ) : isError ? (
              <svg className="w-3.5 h-3.5 text-error" fill="none" stroke="currentColor" strokeWidth={2.5} viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
              </svg>
            ) : isActive ? (
              <span className="relative flex h-2.5 w-2.5">
                <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-accent opacity-60" />
                <span className="relative inline-flex h-2.5 w-2.5 rounded-full bg-accent" />
              </span>
            ) : (
              <Spinner size="sm" />
            )}
          </div>
          <div className="min-w-0">
            <p className="text-xs font-medium text-text-primary truncate">{task.label}</p>
            <p className="text-2xs text-text-muted mt-0.5 truncate font-mono" title={task.model}>{task.model}</p>
          </div>
        </div>
        <div className="text-right shrink-0">
          <p className="text-xs font-semibold text-text-primary tabular-nums">
            {isDone ? '完成' : isError ? '失败' : `${pct}%`}
          </p>
          {task.total > 0 && !isDone && !isError ? (
            <p className="text-2xs text-text-muted tabular-nums mt-0.5">
              {fmtBytes(task.current)} / {fmtBytes(task.total)}
            </p>
          ) : null}
        </div>
      </div>
      {!isDone && !isError && (
        <div className="relative h-1.5 w-full overflow-hidden rounded-full bg-surface-tertiary mt-2">
          {pct > 0 ? (
            <div
              className="h-full rounded-full progress-gradient transition-[width] duration-500 ease-out"
              style={{ width: `${pct}%` }}
            />
          ) : (
            <div
              className="absolute top-0 bottom-0 w-1/3 rounded-full progress-gradient opacity-70"
              style={{ left: '-35%', animation: 'progress-indeterminate 1.4s ease-in-out infinite' }}
            />
          )}
        </div>
      )}
      {isError && task.error && (
        <p className="text-2xs text-error mt-1 truncate" title={task.error}>{task.error}</p>
      )}
    </div>
  )
}

function BotIntroCard({
  emoji, name, tagline, bullets, ready,
}: {
  emoji: string; name: string; tagline: string; bullets: string[]; ready: boolean
}) {
  return (
    <div className="rounded-xl border border-border bg-surface-tertiary/30 px-3.5 py-3 space-y-2">
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2 min-w-0">
          <span className="text-xl shrink-0">{emoji}</span>
          <div className="min-w-0">
            <p className="text-sm font-medium text-text-primary truncate">{name}</p>
            <p className="text-2xs text-text-muted truncate">{tagline}</p>
          </div>
        </div>
        <span className={[
          'text-2xs px-1.5 py-0.5 rounded-full shrink-0',
          ready ? 'bg-success/15 text-success' : 'bg-surface-secondary text-text-muted border border-border',
        ].join(' ')}>
          {ready ? '已就绪' : '准备中'}
        </span>
      </div>
      <ul className="space-y-1 pl-1">
        {bullets.map((b) => (
          <li key={b} className="text-2xs text-text-secondary flex gap-1.5">
            <span className="text-accent shrink-0">•</span>
            <span className="leading-relaxed">{b}</span>
          </li>
        ))}
      </ul>
    </div>
  )
}

// ── helpers ──────────────────────────────────────────────────────────────────

function mkTask(key: TaskKey, label: string, owner: Owner, model: string, repo: string, file: string): Task {
  return { key, label, owner, model, repo, file, status: 'pending', percent: 0, current: 0, total: 0 }
}

// Split a model ref string ("<repo>/<file>") into its repo / file parts.
function splitRef(ref: string): { repo: string; file: string } {
  const parts = ref.split('/')
  if (parts.length < 2) return { repo: '', file: ref }
  const file = parts[parts.length - 1]
  const repo = parts.slice(0, -1).join('/')
  return { repo, file }
}

// Poll the tasks ref until every task is in a terminal state.
async function waitAllDone(ref: { current: Task[] }): Promise<void> {
  const deadline = Date.now() + 2 * 60 * 60 * 1000
  while (Date.now() < deadline) {
    const list = ref.current
    if (list.length > 0 && list.every((t) => t.status === 'success' || t.status === 'error')) return
    await new Promise((r) => setTimeout(r, 400))
  }
}

// Idempotent agent creation: returns the existing agent id if one with the
// same preset name already exists, otherwise creates a new one and returns
// the new id. We key on name because onboarding always uses the canonical
// 对话宝 / 生图宝 names.
async function findAgentByName(name: string): Promise<Agent | null> {
  try {
    const res = await getWs().call<{ agents?: Agent[] }>('agents.list')
    return (res.agents ?? []).find((a) => a.name === name) ?? null
  } catch {
    return null
  }
}

async function ensureChatBotAgent(chatModelRef: string): Promise<string | null> {
  const existing = await findAgentByName(CHAT_BOT_PRESET.name)
  if (existing) return existing.id
  try {
    const res = await getWs().call<{ agent: Agent }>('agents.create', {
      name: CHAT_BOT_PRESET.name,
      role: CHAT_BOT_PRESET.role,
      system_prompt: CHAT_BOT_PRESET.prompt,
      model: chatModelRef,
      max_rounds: 16,
      enabled: true,
      tag: 'to-text',
      allowed_tools: [],
    })
    return res.agent.id
  } catch (e) {
    console.warn('[onboarding] create 对话宝 failed', e)
    return null
  }
}

async function ensureImageBotAgent(
  imageChatRef: string,
  imageVaeRef: string,
  imageDiffRef: string,
): Promise<string | null> {
  const existing = await findAgentByName(IMAGE_BOT_PRESET.name)
  if (existing) return existing.id
  const imageOptions = {
    diffusion_model: imageDiffRef,
    vae: imageVaeRef,
  }
  try {
    const res = await getWs().call<{ agent: Agent }>('agents.create', {
      name: IMAGE_BOT_PRESET.name,
      role: IMAGE_BOT_PRESET.role,
      system_prompt: IMAGE_BOT_PRESET.prompt,
      model: imageChatRef,
      max_rounds: 8,
      enabled: true,
      tag: 'to-image',
      allowed_tools: [],
      options: imageOptions,
    } as Record<string, unknown>)
    return res.agent.id
  } catch (e) {
    console.warn('[onboarding] create 生图宝 failed', e)
    return null
  }
}
