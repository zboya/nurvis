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
// be considered "ready" independently. A `shared` task contributes to both.
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

const IMAGE_CHAT_REF = `${IMAGE_BOT_CHAT_REPO}/${IMAGE_BOT_CHAT_FILE}`
const IMAGE_VAE_REF = `${IMAGE_BOT_VAE_REPO}/${IMAGE_BOT_VAE_FILE}`
const IMAGE_DIFF_REF = `${IMAGE_BOT_DIFFUSION_REPO}/${IMAGE_BOT_DIFFUSION_FILE}`

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
  // Agent ids — populated once each agent's backing models are all downloaded
  // AND the corresponding agents.create call has succeeded. The early-exit
  // buttons only light up after both conditions hold for that agent.
  const [chatBotAgentId, setChatBotAgentId] = useState<string | null>(null)
  const [imageBotAgentId, setImageBotAgentId] = useState<string | null>(null)
  const setActiveAgent = useUiStore((s) => s.setActiveAgent)
  const addPendingAgent = useUiStore((s) => s.addPendingAgent)
  const removePendingAgent = useUiStore((s) => s.removePendingAgent)

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

  // Per-agent readiness watchers — independent, idempotent. As soon as the
  // models needed by 对话宝 (or 生图宝) reach 'success', we create the agent
  // and store its id. This is what lights up the per-bot CTA buttons.
  useEffect(() => {
    if (chatBotAgentId) return
    // 对话宝 needs exactly one model. Both 'chat_bot' and 'shared' satisfy it.
    const chatTask = tasks.find((t) => t.owner === 'chat_bot' || t.owner === 'shared')
    if (!chatTask || chatTask.status !== 'success') return
    let cancelled = false
    ;(async () => {
      const id = await ensureChatBotAgent(chatTask.model)
      if (!cancelled && id) setChatBotAgentId(id)
    })()
    return () => { cancelled = true }
  }, [tasks, chatBotAgentId])

  useEffect(() => {
    if (imageBotAgentId) return
    // 生图宝 needs all 3 image refs ready. Tasks owned by 'image_bot' OR
    // 'shared' (when the chat model overlaps) count toward this.
    const imageBotTasks = tasks.filter((t) => t.owner === 'image_bot' || t.owner === 'shared')
    if (imageBotTasks.length === 0) return
    const allReady = [IMAGE_CHAT_REF, IMAGE_VAE_REF, IMAGE_DIFF_REF].every((ref) => {
      const t = tasks.find((x) => x.model === ref)
      return t?.status === 'success'
    })
    if (!allReady) return
    let cancelled = false
    ;(async () => {
      const id = await ensureImageBotAgent(IMAGE_CHAT_REF, IMAGE_VAE_REF, IMAGE_DIFF_REF)
      if (!cancelled && id) setImageBotAgentId(id)
    })()
    return () => { cancelled = true }
  }, [tasks, imageBotAgentId])

  async function run() {
    try {
      setPhase('detecting')
      try { await getWs().call('runtime.ensure') } catch { /* non-fatal */ }

      const rec = await getWs().call<ModelRecommend>('models.recommend')
      const chatRef =
        rec.default_model ||
        rec.recommended?.[0] ||
        'ggml-org/gemma-3-1b-it-GGUF/gemma-3-1b-it-Q4_K_M.gguf'
      const { repo: chatRepo, file: chatFile } = splitRef(chatRef)

      const built: Task[] = [
        mkTask('chat', '对话宝 · 对话模型', 'chat_bot', chatRef, chatRepo, chatFile),
        mkTask('image_chat', '生图宝 · 对话模型', 'image_bot', IMAGE_CHAT_REF, IMAGE_BOT_CHAT_REPO, IMAGE_BOT_CHAT_FILE),
        mkTask('image_vae', '生图宝 · VAE', 'image_bot', IMAGE_VAE_REF, IMAGE_BOT_VAE_REPO, IMAGE_BOT_VAE_FILE),
        mkTask('image_diffusion', '生图宝 · 扩散模型', 'image_bot', IMAGE_DIFF_REF, IMAGE_BOT_DIFFUSION_REPO, IMAGE_BOT_DIFFUSION_FILE),
      ]
      const deduped: Task[] = []
      for (const t of built) {
        const existing = deduped.find((x) => x.model === t.model)
        if (existing) {
          existing.owner = 'shared'
          continue
        }
        deduped.push(t)
      }
      setTasks(deduped)
      setPhase('downloading')

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

      await waitAllDone(tasksRef)

      const failed = tasksRef.current.filter((t) => t.status === 'error')
      if (failed.length > 0) {
        setError(`部分模型下载失败：${failed.map((f) => f.label).join('、')}`)
        setPhase('error')
        return
      }

      // Make sure both agents exist (idempotent — usually a no-op because the
      // per-agent watchers already created them).
      setPhase('finalizing')
      const chatId = await ensureChatBotAgent(chatRef)
      if (chatId) setChatBotAgentId(chatId)
      const imgId = await ensureImageBotAgent(IMAGE_CHAT_REF, IMAGE_VAE_REF, IMAGE_DIFF_REF)
      if (imgId) setImageBotAgentId(imgId)

      setPhase('done')
      setTimeout(() => onComplete(), 800)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      setPhase('error')
    }
  }

  // Enter the main app immediately with one of the two bots already selected.
  // Whichever bot is NOT yet ready will continue downloading in the background;
  // its agent will be auto-created once all of its models finish (via a poll
  // running detached from this component's lifecycle).
  function enterAppWith(agentId: string) {
    setActiveAgent(agentId)
    if (!chatBotAgentId) scheduleChatBotCreationInBackground()
    if (!imageBotAgentId) scheduleImageBotCreationInBackground()
    onComplete()
  }

  // Skip the wait entirely, even if no bot is ready yet. Enter the main app
  // with whichever bot is ready (if any) selected; all remaining bots become
  // "pending" and will be created in the background once their models finish.
  function skipAndEnter() {
    const firstReady = chatBotAgentId || imageBotAgentId
    if (firstReady) setActiveAgent(firstReady)
    if (!chatBotAgentId) scheduleChatBotCreationInBackground()
    if (!imageBotAgentId) scheduleImageBotCreationInBackground()
    onComplete()
  }

  // Background poll: keep watching models.list and create the agent once its
  // model(s) are present. Both functions are idempotent thanks to
  // findAgentByName guards inside ensure*Agent. They also register the agent
  // as "pending" in the global ui-store so the main app can surface a hint
  // until creation succeeds.
  function scheduleChatBotCreationInBackground() {
    const chatTask = tasksRef.current.find((t) => t.owner === 'chat_bot' || t.owner === 'shared')
    const ref = chatTask?.model
    if (!ref) return
    addPendingAgent({
      key: 'chat_bot',
      emoji: CHAT_BOT_PRESET.emoji,
      name: CHAT_BOT_PRESET.name,
      hint: '对话模型仍在下载，下载完成后将自动创建',
    })
    ;(async () => {
      const deadline = Date.now() + 4 * 60 * 60 * 1000
      while (Date.now() < deadline) {
        try {
          if (await modelInstalled(ref)) {
            const id = await ensureChatBotAgent(ref)
            if (id) removePendingAgent('chat_bot')
            return
          }
        } catch { /* keep polling */ }
        await new Promise((r) => setTimeout(r, 5000))
      }
      // Timed out — drop the pending marker so the user isn't misled forever.
      removePendingAgent('chat_bot')
    })()
  }

  function scheduleImageBotCreationInBackground() {
    const needed = [IMAGE_CHAT_REF, IMAGE_VAE_REF, IMAGE_DIFF_REF]
    addPendingAgent({
      key: 'image_bot',
      emoji: IMAGE_BOT_PRESET.emoji,
      name: IMAGE_BOT_PRESET.name,
      hint: '扩散模型与 VAE 仍在下载，完成后将自动创建',
    })
    ;(async () => {
      const deadline = Date.now() + 4 * 60 * 60 * 1000
      while (Date.now() < deadline) {
        try {
          const checks = await Promise.all(needed.map((n) => modelInstalled(n)))
          if (checks.every(Boolean)) {
            const id = await ensureImageBotAgent(IMAGE_CHAT_REF, IMAGE_VAE_REF, IMAGE_DIFF_REF)
            if (id) removePendingAgent('image_bot')
            return
          }
        } catch { /* keep polling */ }
        await new Promise((r) => setTimeout(r, 5000))
      }
      removePendingAgent('image_bot')
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

  const etaSeconds = speedBps > 0 && remainingBytes > 0 ? remainingBytes / speedBps : 0
  const chatBotReady = !!chatBotAgentId
  const imageBotReady = !!imageBotAgentId
  // The "ready helpers" panel shows whenever *anything* is ready and we're
  // still in active phases (any non-error phase before user exits).
  const showReadyPanel =
    (chatBotReady || imageBotReady) &&
    (phase === 'downloading' || phase === 'finalizing')

  return (
    <div className="space-y-5 animate-slide-up">
      {/* Friendly hint */}
      <div className="bg-accent/5 border border-accent/20 rounded-xl p-4">
        <p className="text-sm text-text-primary font-medium">正在为你准备 AI 助手</p>
        <p className="text-xs text-text-muted mt-1 leading-relaxed">
          我们会自动下载「对话宝」和「生图宝」运行所需的模型（约数 GB），
          下载时间取决于你的网络。任意一个助手就绪后即可立即开始使用，另一个会在后台继续下载。
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

          {/* Per-agent CTA: any ready bot can be entered right away. */}
          {showReadyPanel && (
            <div className="space-y-2">
              {chatBotReady && (
                <ReadyAgentCTA
                  emoji={CHAT_BOT_PRESET.emoji}
                  name={CHAT_BOT_PRESET.name}
                  hint={
                    imageBotReady
                      ? '点此进入应用，开始聊天'
                      : '生图宝仍在后台下载，完成后会自动启用'
                  }
                  buttonText="先用对话宝聊起来 →"
                  onClick={() => chatBotAgentId && enterAppWith(chatBotAgentId)}
                />
              )}
              {imageBotReady && (
                <ReadyAgentCTA
                  emoji={IMAGE_BOT_PRESET.emoji}
                  name={IMAGE_BOT_PRESET.name}
                  hint={
                    chatBotReady
                      ? '点此进入应用，开始创作'
                      : '对话宝仍在后台下载，完成后会自动启用'
                  }
                  buttonText="先用生图宝创作 →"
                  onClick={() => imageBotAgentId && enterAppWith(imageBotAgentId)}
                />
              )}
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
            <Button variant="ghost" onClick={skipAndEnter}>跳过下载，先进入应用</Button>
          </div>
        </div>
      )}

      {/* Always-available escape hatch: let the user enter the app even if
          nothing finished yet. Hidden once everything is genuinely done. */}
      {phase !== 'done' && phase !== 'error' && (
        <div className="flex items-center justify-between gap-3 rounded-xl border border-border bg-surface-tertiary/30 px-3.5 py-2.5">
          <div className="min-w-0">
            <p className="text-xs font-medium text-text-primary">不想等下载？</p>
            <p className="text-2xs text-text-muted mt-0.5 leading-relaxed">
              可以直接进入应用，未完成的模型会在后台继续下载，
              对应的智能体将在下载完成后自动创建。
            </p>
          </div>
          <Button variant="ghost" size="md" onClick={skipAndEnter} className="shrink-0">
            跳过，先进入 →
          </Button>
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
              ready={imageBotReady}
            />
          </div>
        </div>
      )}
    </div>
  )
}

function ReadyAgentCTA({
  emoji, name, hint, buttonText, onClick,
}: {
  emoji: string; name: string; hint: string; buttonText: string; onClick: () => void
}) {
  return (
    <div className="flex items-center justify-between gap-3 rounded-xl border border-accent/30 bg-accent/10 px-3.5 py-3">
      <div className="flex items-center gap-2.5 min-w-0">
        <span className="text-xl shrink-0">{emoji}</span>
        <div className="min-w-0">
          <p className="text-xs font-medium text-text-primary flex items-center gap-1.5">
            {name}
            <span className="text-2xs px-1.5 py-0.5 bg-success/15 text-success rounded-full font-normal">
              已就绪
            </span>
          </p>
          <p className="text-2xs text-text-muted mt-0.5 truncate">{hint}</p>
        </div>
      </div>
      <Button variant="primary" size="md" onClick={onClick}>
        {buttonText}
      </Button>
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

function splitRef(ref: string): { repo: string; file: string } {
  const parts = ref.split('/')
  if (parts.length < 2) return { repo: '', file: ref }
  const file = parts[parts.length - 1]
  const repo = parts.slice(0, -1).join('/')
  return { repo, file }
}

async function waitAllDone(ref: { current: Task[] }): Promise<void> {
  const deadline = Date.now() + 2 * 60 * 60 * 1000
  while (Date.now() < deadline) {
    const list = ref.current
    if (list.length > 0 && list.every((t) => t.status === 'success' || t.status === 'error')) return
    await new Promise((r) => setTimeout(r, 400))
  }
}

// modelInstalled checks whether a given "repo/file" reference is present in
// the local model registry. `models.list` returns rows whose `name` field is
// just the file basename, so a naive `have.has("repo/file")` always misses.
// We accept either form: prefer matching by `repo+'/'+file`, and fall back to
// matching by file basename for legacy rows that didn't persist the repo.
async function modelInstalled(ref: string): Promise<boolean> {
  const file = ref.split('/').pop() ?? ref
  const res = await getWs().call<{ models?: Array<{ name?: string; repo?: string; file?: string }> }>(
    'models.list',
  )
  for (const m of res.models ?? []) {
    const repoFile = m.repo && m.file ? `${m.repo}/${m.file}` : ''
    if (repoFile === ref) return true
    // Fall back to file-basename match (only safe when refs across different
    // repos don't share filenames, which is true for our onboarding set).
    if (!m.repo && (m.name === file || m.file === file)) return true
    if (m.name === file && m.file === file) return true
  }
  return false
}

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
