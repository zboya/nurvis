import { useState, useEffect, useCallback } from 'react'
import { getWs } from '../lib/ws'
import type { Session } from '../types'

export function useSessions(agentId: string | null) {
  const [sessions, setSessions] = useState<Session[]>([])
  const [loading, setLoading] = useState(false)

  const load = useCallback(async () => {
    if (!agentId) { setSessions([]); return }
    setLoading(true)
    try {
      const res = await getWs().call<{ sessions: Session[] }>('sessions.list', { agent_id: agentId })
      setSessions(res.sessions ?? [])
    } catch { setSessions([]) } finally { setLoading(false) }
  }, [agentId])

  useEffect(() => { load() }, [load])

  // Refresh list when an agent run completes (backend auto-sets session label)
  useEffect(() => {
    const ws = getWs()
    const unsub = ws.on('agent.run.completed', () => {
      load()
    })
    return unsub
  }, [load])

  const create = useCallback(async (agentId: string): Promise<Session> => {
    const res = await getWs().call<{ session: Session }>('sessions.create', { agent_id: agentId })
    setSessions((prev) => [res.session, ...prev])
    return res.session
  }, [])

  const remove = useCallback(async (id: string) => {
    await getWs().call('sessions.delete', { id })
    setSessions((prev) => prev.filter((s) => s.id !== id))
  }, [])

  return { sessions, loading, load, create, remove }
}
