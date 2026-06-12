import { useEffect } from 'react'
import { getWs } from '../lib/ws'
import { useModelStore } from '../stores/model-store'
import type { PullProgress, ModelRow } from '../types'

/**
 * Global subscription to `models.pull.progress` events. Mount once at the app
 * root so background pulls stay tracked even when the Settings panel that
 * triggered them is closed.
 *
 * On (re)connect, also fetches `models.pull_list` so the UI can display
 * downloads that were interrupted by a previous restart, plus any pull that
 * is still progressing in the backend.
 *
 * Important: at the very first render, the WebSocket client may not yet be
 * initialized (initWs runs inside an async effect in App). Calling getWs()
 * unconditionally would throw and crash the React tree (white screen).
 * We poll briefly until the client is ready, then subscribe.
 */
export function useModelSubscription() {
  const update = useModelStore((s) => s.update)
  const hydrate = useModelStore((s) => s.hydrate)

  useEffect(() => {
    let unsub: (() => void) | undefined
    let cancelled = false
    let timer: ReturnType<typeof setInterval> | undefined

    const trySubscribe = () => {
      if (cancelled) return false
      try {
        const ws = getWs()
        unsub = ws.on('models.pull.progress', (raw) => {
          const p = raw as PullProgress
          if (!p?.model) return
          update(p)
        })
        // Best-effort initial load. If it fails (e.g. backend still starting),
        // ignore — the user can refresh later.
        ws.call<{ pulls: ModelRow[] }>('models.pull_list')
          .then((r) => hydrate(r.pulls ?? []))
          .catch(() => {})
        return true
      } catch {
        return false
      }
    }

    if (!trySubscribe()) {
      timer = setInterval(() => {
        if (trySubscribe() && timer) {
          clearInterval(timer)
          timer = undefined
        }
      }, 200)
    }

    return () => {
      cancelled = true
      if (timer) clearInterval(timer)
      if (unsub) unsub()
    }
  }, [update, hydrate])
}
