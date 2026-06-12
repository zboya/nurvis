import { useState, useEffect } from 'react'
import { getWs } from '../lib/ws'
import type { ModelRecommend, RuntimeStatus } from '../types'

// useRuntime queries the local llama.cpp runtime status alongside model
// recommendations. Replaces the legacy useOllama hook.
export function useRuntime() {
  const [status, setStatus] = useState<RuntimeStatus | null>(null)
  const [recommend, setRecommend] = useState<ModelRecommend | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    const ws = getWs()
    Promise.all([
      ws.call<RuntimeStatus>('runtime.status'),
      ws.call<ModelRecommend>('models.recommend'),
    ]).then(([s, r]) => {
      setStatus(s)
      setRecommend(r)
    }).catch(console.error).finally(() => setLoading(false))
  }, [])

  return { status, recommend, loading }
}

// Backwards-compat alias for the rare callers that still import the old name.
export const useOllama = useRuntime
