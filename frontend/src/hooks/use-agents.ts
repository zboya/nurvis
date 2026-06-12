import { useEffect, useCallback } from 'react'
import { create } from 'zustand'
import { getWs } from '../lib/ws'
import type { Agent } from '../types'

interface AgentsState {
  agents: Agent[]
  loading: boolean
  loaded: boolean
  inflight: Promise<void> | null
  load: () => Promise<void>
  upsert: (agent: Agent) => void
  removeLocal: (id: string) => void
}

const useAgentsStore = create<AgentsState>((set, get) => ({
  agents: [],
  loading: true,
  loaded: false,
  inflight: null,
  load: async () => {
    const existing = get().inflight
    if (existing) return existing

    set({ loading: true })
    const p = (async () => {
      try {
        const res = await getWs().call<{ agents: Agent[] }>('agents.list')
        set({ agents: res.agents ?? [], loaded: true })
      } catch (e) {
        console.error('agents.list failed', e)
        set({ agents: [], loaded: true })
      } finally {
        set({ loading: false, inflight: null })
      }
    })()
    set({ inflight: p })
    return p
  },
  upsert: (agent) => {
    set((state) => {
      const exists = state.agents.some((a) => a.id === agent.id)
      return {
        agents: exists
          ? state.agents.map((a) => (a.id === agent.id ? agent : a))
          : [...state.agents, agent],
      }
    })
  },
  removeLocal: (id) => set((state) => ({ agents: state.agents.filter((a) => a.id !== id) })),
}))

export function useAgents() {
  const agents = useAgentsStore((s) => s.agents)
  const loading = useAgentsStore((s) => s.loading)
  const loaded = useAgentsStore((s) => s.loaded)
  const load = useAgentsStore((s) => s.load)
  const upsert = useAgentsStore((s) => s.upsert)
  const removeLocal = useAgentsStore((s) => s.removeLocal)

  useEffect(() => {
    if (!loaded) load()
  }, [loaded, load])

  const create = useCallback(async (input: Partial<Agent>) => {
    const res = await getWs().call<{ agent: Agent }>('agents.create', input as Record<string, unknown>)
    upsert(res.agent)
    return res.agent
  }, [upsert])

  const update = useCallback(async (id: string, input: Partial<Agent>) => {
    const res = await getWs().call<{ agent: Agent }>('agents.update', { id, ...input } as Record<string, unknown>)
    upsert(res.agent)
    return res.agent
  }, [upsert])

  const remove = useCallback(async (id: string) => {
    await getWs().call('agents.delete', { id })
    removeLocal(id)
  }, [removeLocal])

  return { agents, loading, load, create, update, remove }
}
