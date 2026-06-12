import { useEffect, useCallback } from 'react'
import { create } from 'zustand'
import { getWs } from '../lib/ws'
import type { Project } from '../types'

// ─────────────────────────────────────────────────────────────────────────────
// Global projects store
//
// Why a store instead of per-component useState:
//   Multiple components (Sidebar, ChatCanvas top bar, SettingsPanel) all need
//   the projects list. If each kept its own local state, mutations in one
//   (e.g. Sidebar creating/selecting a new project) would not propagate to the
//   others, leading to stale UI such as the chat top-bar project badge missing
//   until a manual page reload. A single Zustand store ensures all subscribers
//   re-render on any change.
// ─────────────────────────────────────────────────────────────────────────────

interface ProjectsState {
  projects: Project[]
  loading: boolean
  loaded: boolean
  inflight: Promise<void> | null
  load: () => Promise<void>
}

const useProjectsStore = create<ProjectsState>((set, get) => ({
  projects: [],
  loading: true,
  loaded: false,
  inflight: null,
  load: async () => {
    // Deduplicate concurrent loads triggered by multiple components mounting
    // at roughly the same time.
    const existing = get().inflight
    if (existing) return existing
    const ws = getWs()
    set({ loading: true })
    const p = (async () => {
      try {
        const res = await ws.call<{ projects: Project[] }>('projects.list')
        set({ projects: res.projects ?? [], loaded: true })
      } catch {
        set({ projects: [], loaded: true })
      } finally {
        set({ loading: false, inflight: null })
      }
    })()
    set({ inflight: p })
    return p
  },
}))

export function useProjects() {
  const ws = getWs()
  const projects = useProjectsStore((s) => s.projects)
  const loading = useProjectsStore((s) => s.loading)
  const loaded = useProjectsStore((s) => s.loaded)
  const load = useProjectsStore((s) => s.load)

  // Trigger initial load once across all components
  useEffect(() => {
    if (!loaded) load()
  }, [loaded, load])

  const createProject = useCallback(async (name: string, dir: string, description?: string): Promise<Project | null> => {
    try {
      const res = await ws.call<Project>('projects.create', { name, dir, description })
      await load()
      return res
    } catch {
      return null
    }
  }, [ws, load])

  const deleteProject = useCallback(async (id: string) => {
    await ws.call('projects.delete', { id })
    await load()
  }, [ws, load])

  const updateProject = useCallback(async (id: string, name: string, dir: string, description?: string) => {
    try {
      await ws.call('projects.update', { id, name, dir, description })
      await load()
    } catch { /* ignore */ }
  }, [ws, load])

  return { projects, loading, createProject, deleteProject, updateProject, reload: load }
}
