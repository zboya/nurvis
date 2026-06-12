import { useEffect, useState } from 'react'
import { getWs } from '../lib/ws'

interface CapabilitiesResp {
  model: string
  capabilities: string[]
  vision: boolean
}

// Module-level cache: same model is queried only once per session.
const cache = new Map<string, CapabilitiesResp>()
const inflight = new Map<string, Promise<CapabilitiesResp>>()

/**
 * Query model capabilities (vision/tools/embed etc.). Results are cached at module level.
 * - Returns undefined when model is empty.
 * - On probe failure, returns vision=false and preserves the error.
 */
export function useModelCapabilities(model: string | undefined) {
  const [data, setData] = useState<CapabilitiesResp | undefined>(
    model ? cache.get(model) : undefined,
  )
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!model) {
      setData(undefined)
      setError(null)
      return
    }
    const cached = cache.get(model)
    if (cached) {
      setData(cached)
      setError(null)
      return
    }
    let cancelled = false
    setLoading(true)
    setError(null)

    const ws = getWs()
    let p = inflight.get(model)
    if (!p) {
      p = ws.call<CapabilitiesResp>('models.capabilities', { model })
        .then((res) => {
          cache.set(model, res)
          return res
        })
        .finally(() => inflight.delete(model))
      inflight.set(model, p)
    }

    p.then((res) => {
      if (cancelled) return
      setData(res)
    }).catch((e: Error) => {
      if (cancelled) return
      // Failure fallback: mark vision as unsupported to avoid sending images by mistake.
      const fallback: CapabilitiesResp = { model, capabilities: [], vision: false }
      setData(fallback)
      setError(e.message ?? 'unknown error')
    }).finally(() => {
      if (!cancelled) setLoading(false)
    })

    return () => { cancelled = true }
  }, [model])

  return { data, loading, error, vision: data?.vision }
}
