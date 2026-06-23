import { create } from 'zustand'
import { persist } from 'zustand/middleware'

type Theme = 'dark' | 'light'
type View = 'chat' | 'settings'

// A "pending agent" is one whose underlying models are still downloading in
// the background after the user skipped the onboarding wait. Once its models
// finish and the agent is created, it should be removed from this list.
export interface PendingAgent {
  key: string        // stable identifier: 'chat_bot' | 'image_bot'
  emoji: string
  name: string
  hint: string       // short status hint, e.g. "对话模型下载中…"
}

interface UiState {
  theme: Theme
  view: View
  sidebarOpen: boolean
  activeAgentId: string | null
  activeSessionId: string | null
  activeProjectId: string | null

  sessionResetKey: number
  incrementSessionResetKey: () => void

  // Agents that are still being created in the background after onboarding skip.
  pendingAgents: PendingAgent[]
  addPendingAgent: (a: PendingAgent) => void
  updatePendingAgent: (key: string, patch: Partial<PendingAgent>) => void
  removePendingAgent: (key: string) => void
  clearPendingAgents: () => void

  setTheme: (t: Theme) => void
  toggleTheme: () => void
  setView: (v: View) => void
  toggleSidebar: () => void
  setActiveAgent: (id: string | null) => void
  setActiveSession: (id: string | null) => void
  setActiveProject: (id: string | null) => void
}

export const useUiStore = create<UiState>()(
  persist(
    (set, get) => ({
      theme: 'dark',
      view: 'chat',
      sidebarOpen: true,
      activeAgentId: null,
      activeSessionId: null,
      activeProjectId: null,
      sessionResetKey: 0,
      incrementSessionResetKey: () => set((s) => ({ sessionResetKey: s.sessionResetKey + 1 })),

      pendingAgents: [],
      addPendingAgent: (a) =>
        set((s) =>
          s.pendingAgents.some((p) => p.key === a.key)
            ? s
            : { pendingAgents: [...s.pendingAgents, a] },
        ),
      updatePendingAgent: (key, patch) =>
        set((s) => ({
          pendingAgents: s.pendingAgents.map((p) => (p.key === key ? { ...p, ...patch } : p)),
        })),
      removePendingAgent: (key) =>
        set((s) => ({ pendingAgents: s.pendingAgents.filter((p) => p.key !== key) })),
      clearPendingAgents: () => set({ pendingAgents: [] }),

      setTheme: (t) => set({ theme: t }),
      toggleTheme: () => set({ theme: get().theme === 'dark' ? 'light' : 'dark' }),
      setView: (v) => set({ view: v }),
      toggleSidebar: () => set({ sidebarOpen: !get().sidebarOpen }),
      setActiveAgent: (id) => set({ activeAgentId: id }),
      setActiveSession: (id) => set({ activeSessionId: id }),
      setActiveProject: (id) => set({ activeProjectId: id }),
    }),
    {
      name: 'nurvis:ui',
      partialize: (s) => ({ theme: s.theme }),
    },
  ),
)
