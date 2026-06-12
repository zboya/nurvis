// Global Gateway address resolution.
//
// Desktop (Wails3): Uses Service binding `GetGatewayAddr()` to get the real listen address from Go.
//   - Default config ListenAddr=":18981"; empty host is treated as 127.0.0.1 for local connections.
//   - Backend can be overridden by NURVIS_ADDR env var for port/IP; frontend doesn't need to care.
// Browser dev mode (vite dev / direct dist): falls back to `<current host>:18981`.
//
// Note: Wails binding is async (Call.ByID), so this module only exposes resolver functions
// instead of top-level constants; callers must await once at startup then initWs(...).

import { GetGatewayAddr } from '../../bindings/github.com/zboya/nurvis/cmd/nurvis-desktop/service'

const FALLBACK_PORT = 18981

interface GatewayUrls {
  ws: string
  http: string
}

function isWailsRuntime(): boolean {
  return typeof window !== 'undefined' && '_wails' in window
}

function normalizeAddr(addr: string): string {
  // Backend default returns ":18981" or "127.0.0.1:18981"; frontend needs to fill in the host.
  const trimmed = addr.trim()
  if (!trimmed) return `127.0.0.1:${FALLBACK_PORT}`
  if (trimmed.startsWith(':')) return `127.0.0.1${trimmed}`
  return trimmed
}

function buildUrls(addr: string): GatewayUrls {
  const a = normalizeAddr(addr)
  return {
    http: `http://${a}`,
    ws: `ws://${a}/ws`,
  }
}

function browserFallback(): GatewayUrls {
  const host =
    typeof window !== 'undefined' && window.location.hostname
      ? window.location.hostname
      : '127.0.0.1'
  return buildUrls(`${host}:${FALLBACK_PORT}`)
}

let cached: GatewayUrls | null = null

/**
 * Resolve Gateway connection address:
 * - Wails env calls service.GetGatewayAddr() (async Service binding);
 * - Non-Wails env (browser dev) falls back to current host:18981.
 * Results are cached; repeated calls are cheap.
 */
export async function resolveGatewayUrls(): Promise<GatewayUrls> {
  if (cached) return cached
  if (isWailsRuntime()) {
    try {
      const addr = await GetGatewayAddr()
      cached = buildUrls(addr)
      return cached
    } catch (e) {
      console.warn('[gateway] GetGatewayAddr failed, fallback to localhost', e)
    }
  }
  cached = browserFallback()
  return cached
}

/** Force-reset the cache (typically for debugging / reconnection scenarios). */
export function resetGatewayUrlCache(): void {
  cached = null
}
