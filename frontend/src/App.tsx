import { useEffect, useState, useCallback, useRef } from 'react'
import { useUiStore } from './stores/ui-store'
import { initWs, getWs } from './lib/ws'
import { resolveGatewayUrls } from './lib/constants'
import { OnboardingWizard } from './components/onboarding/OnboardingWizard'
import { AppShell } from './components/layout/AppShell'
import { Spinner } from './components/ui'
import { useModelSubscription } from './hooks/use-model'

function SplashScreen() {
  return (
    <div className="h-dvh flex flex-col items-center justify-center app-bg">
      <div className="flex flex-col items-center gap-4 animate-fade-in">
        <div className="w-16 h-16 rounded-2xl bg-gradient-to-br from-accent/30 to-purple-500/30 border border-accent/20 flex items-center justify-center text-4xl shadow-xl">
          🤖
        </div>
        <Spinner size="md" className="border-white/40 border-t-white/10" />
        <p className="text-sm text-white/50">正在连接…</p>
      </div>
    </div>
  )
}

export default function App() {
  const theme = useUiStore((s) => s.theme)
  // onboarded no longer stored in localStorage; now read from backend settings
  const [onboarded, setOnboardedLocal] = useState<boolean | null>(null) // null=loading

  // Subscribe to background model pull progress globally so the Settings panel
  // (or any consumer) can render up-to-date progress regardless of mount state.
  useModelSubscription()

  // Apply theme
  useEffect(() => {
    const apply = (t: string) => {
      if (t === 'dark') {
        document.documentElement.classList.add('dark')
      } else if (t === 'light') {
        document.documentElement.classList.remove('dark')
      } else {
        const isDark = window.matchMedia('(prefers-color-scheme: dark)').matches
        document.documentElement.classList.toggle('dark', isDark)
      }
    }
    apply(theme)
  }, [theme])

  // Connect to gateway
  const settledRef = useRef(false)
  useEffect(() => {
    let unsub: (() => void) | undefined
    let timer: ReturnType<typeof setTimeout> | undefined
    let cancelled = false

    ;(async () => {
      const { ws: wsUrl } = await resolveGatewayUrls()
      if (cancelled) return
      const ws = initWs(wsUrl)
      unsub = ws.onConnectionChange(async (connected) => {
        if (!connected || cancelled) return
        // After WS connection, read onboarded status from backend
        try {
          const res = await getWs().call<{ value: boolean | null }>('settings.get', { key: 'onboarded' })
          if (!cancelled) {
            settledRef.current = true
            setOnboardedLocal(res.value === true)
          }
        } catch {
          if (!cancelled) {
            settledRef.current = true
            setOnboardedLocal(false)
          }
        }
      })
      // Fallback timeout: only applies when backend value hasn't arrived yet
      timer = setTimeout(() => {
        if (!cancelled && !settledRef.current) {
          settledRef.current = true
          setOnboardedLocal(false)
        }
      }, 5000)
    })()

    return () => {
      cancelled = true
      if (timer) clearTimeout(timer)
      if (unsub) unsub()
    }
  }, [])

  // Keyboard shortcuts
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      const mod = e.metaKey || e.ctrlKey
      if (mod && e.key === 'b') {
        e.preventDefault()
        useUiStore.getState().toggleSidebar()
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [])

  const handleOnboardingComplete = useCallback(async () => {
    // If neither an agent exists nor a pending creation is queued, the
    // onboarding hasn't really produced anything actionable yet — stay on
    // the wizard. Otherwise mark onboarded so the user lands in the app.
    try {
      const res = await getWs().call<{ agents?: unknown[] }>('agents.list')
      const hasAgent = (res.agents?.length ?? 0) > 0
      const hasPending = useUiStore.getState().pendingAgents.length > 0
      if (!hasAgent && !hasPending) {
        setOnboardedLocal(false)
        return
      }
    } catch { /* ignore */ }
    // Write to backend settings
    await getWs().call('settings.set', { key: 'onboarded', value: true }).catch(() => {})
    setOnboardedLocal(true)
  }, [])

  if (onboarded === null) return <SplashScreen />

  if (!onboarded) {
    return <OnboardingWizard onComplete={handleOnboardingComplete} />
  }

  return <AppShell />
}