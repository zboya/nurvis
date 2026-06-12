import { create } from 'zustand'
import { persist } from 'zustand/middleware'

type Theme = 'dark' | 'light'
type View = 'chat' | 'settings'

interface UiState {
  theme: Theme
  view: View
  sidebarOpen: boolean
  // Currently selected agent id
  activeAgentId: string | null
  // Currently selected session id
  activeSessionId: string | null
  // Currently selected project id (carried during chat)
  activeProjectId: string | null

  // Incremented when creating a new session, forcing useChat to clear
  sessionResetKey: number
  incrementSessionResetKey: () => void

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
