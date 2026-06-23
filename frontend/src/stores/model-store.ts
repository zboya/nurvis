import { create } from 'zustand'
import type { PullProgress, ModelRow } from '../types'

// State per model. Keyed by model name. Survives component unmount so that
// users can navigate away from the Settings panel and the pull keeps going.
export interface ModelState {
  model: string
  // Optional repo/file for retry; populated when state is rehydrated from the
  // backend, or fillable from the canonical "<repo>/<file>" model id.
  repo?: string
  file?: string
  status: string         // raw backend status (queued / resolving / downloading / verifying / success / error / interrupted)
  percent: number        // 0..100
  current: number
  total: number
  done: boolean          // true when status == "success"
  interrupted?: boolean  // true when last process restart left the row mid-flight
  error?: string
  startedAt: number
  finishedAt?: number
}

interface ModelStore {
  // Map of model name → progress snapshot. Use a plain object for simple shallow comparison.
  pulls: Record<string, ModelState>

  // Start (or restart) a pull entry. Called right after models.pull RPC returns.
  start: (model: string, repo?: string, file?: string) => void
  // Update progress from a `models.pull.progress` event.
  update: (p: PullProgress) => void
  // Mark a pull as failed (e.g. RPC rejected).
  fail: (model: string, error: string) => void
  // Remove a finished/failed entry once acknowledged by UI.
  dismiss: (model: string) => void
  // Replace the whole map from a backend `models.pull_list` response.
  hydrate: (rows: ModelRow[]) => void
  // True if any model is currently being pulled (not done & not errored).
  hasActive: () => boolean
}

export const useModelStore = create<ModelStore>()((set, get) => ({
  pulls: {},

  start: (model, repo, file) =>
    set((s) => ({
      pulls: {
        ...s.pulls,
        [model]: {
          model,
          repo: repo ?? s.pulls[model]?.repo,
          file: file ?? s.pulls[model]?.file,
          status: 'queued',
          percent: 0,
          current: 0,
          total: 0,
          done: false,
          interrupted: false,
          error: undefined,
          startedAt: Date.now(),
        },
      },
    })),

  update: (p) =>
    set((s) => {
      if (!p?.model) return s
      const prev = s.pulls[p.model]
      const lower = (p.status ?? '').toLowerCase()
      const isError = !!p.error || lower === 'error'
      const isSuccess = !isError && lower.includes('success')
      // Late-arriving event for an entry the user explicitly dismissed could
      // be ignored, but we can't tell "never seen" from "dismissed" here.
      // Accept missing prev so events from pulls kicked off outside this tab
      // (e.g. the onboarding wizard) still land in the store correctly.
      if (!prev) {
        // Try to recover repo/file from the canonical "repo/file" id.
        let repo: string | undefined
        let file: string | undefined
        const parts = p.model.split('/')
        if (parts.length >= 2) {
          file = parts[parts.length - 1]
          repo = parts.slice(0, -1).join('/')
        }
        return {
          pulls: {
            ...s.pulls,
            [p.model]: {
              model: p.model,
              repo,
              file,
              status: p.status ?? 'downloading',
              percent: typeof p.percent === 'number' ? p.percent : (isSuccess ? 100 : 0),
              current: typeof p.current === 'number' ? p.current : 0,
              total: typeof p.total === 'number' ? p.total : 0,
              done: isSuccess,
              interrupted: false,
              error: isError ? (p.error || '拉取失败') : undefined,
              startedAt: Date.now(),
              finishedAt: isSuccess || isError ? Date.now() : undefined,
            },
          },
        }
      }
      const next: ModelState = {
        ...prev,
        status: p.status ?? prev.status,
        percent: typeof p.percent === 'number' ? p.percent : (isSuccess ? 100 : prev.percent),
        current: typeof p.current === 'number' ? p.current : prev.current,
        total: typeof p.total === 'number' ? p.total : prev.total,
        done: isSuccess || prev.done,
        // A live progress event always clears the "interrupted" flag.
        interrupted: false,
        error: isError ? (p.error || prev.error || '拉取失败') : prev.error,
        finishedAt: isSuccess || isError ? Date.now() : prev.finishedAt,
      }
      return { pulls: { ...s.pulls, [p.model]: next } }
    }),

  fail: (model, error) =>
    set((s) => {
      const prev = s.pulls[model]
      if (!prev) return s
      return {
        pulls: {
          ...s.pulls,
          [model]: { ...prev, error, done: false, finishedAt: Date.now() },
        },
      }
    }),

  dismiss: (model) =>
    set((s) => {
      if (!(model in s.pulls)) return s
      const next = { ...s.pulls }
      delete next[model]
      return { pulls: next }
    }),

  hydrate: (rows) =>
    set((s) => {
      const next: Record<string, ModelState> = { ...s.pulls }
      for (const r of rows) {
        const prev = next[r.model]
        const lower = (r.status ?? '').toLowerCase()
        const isError = lower === 'error'
        const isSuccess = lower === 'success'
        const isInterrupted = lower === 'interrupted'
        const isTerminal = isError || isSuccess || isInterrupted
        // Skip when local already has a fresher non-terminal progress AND the
        // backend hasn't reached a terminal state yet. If the backend is
        // terminal (success/error/interrupted), always overwrite — this lets
        // the UI recover when a `models.pull.progress` success event was
        // missed (e.g. fired before the global subscription mounted).
        if (prev && !prev.done && !prev.error && !prev.interrupted && !isTerminal) continue
        next[r.model] = {
          model: r.model,
          repo: r.repo,
          file: r.file,
          status: r.status,
          percent: isSuccess ? 100 : (r.percent ?? 0),
          current: r.current ?? 0,
          total: r.total ?? 0,
          done: isSuccess,
          interrupted: isInterrupted,
          error: isError ? (r.error || '拉取失败')
            : isInterrupted ? (r.error || '进程已重启，下载已中断')
            : undefined,
          startedAt: r.started_at || prev?.startedAt || Date.now(),
          finishedAt: r.finished_at || (isTerminal ? Date.now() : prev?.finishedAt),
        }
      }
      return { pulls: next }
    }),

  hasActive: () => {
    const pulls = get().pulls
    for (const k in pulls) {
      const v = pulls[k]
      if (!v.done && !v.error && !v.interrupted) return true
    }
    return false
  },
}))
