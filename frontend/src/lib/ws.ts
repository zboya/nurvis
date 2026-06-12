// WebSocket JSON-RPC client for Nurvis Gateway
// Frame format fully aligned with AGENTS.md §12

export type FrameType = 'req' | 'res' | 'event'

export interface RequestFrame {
  type: 'req'
  id: string
  method: string
  params?: Record<string, unknown>
}

export interface ResponseFrame {
  type: 'res'
  id: string
  ok: boolean
  payload?: unknown
  error?: { code: string; message: string; retryable?: boolean }
}

export interface EventFrame {
  type: 'event'
  event: string
  payload: unknown
}

export type Frame = RequestFrame | ResponseFrame | EventFrame
export type EventHandler = (payload: unknown) => void

interface PendingRequest {
  resolve: (value: unknown) => void
  reject: (err: Error) => void
  timer: ReturnType<typeof setTimeout>
}

const DEFAULT_TIMEOUT = 15_000
const CHAT_TIMEOUT = 120_000
const RECONNECT_BASE = 800
const RECONNECT_MAX = 10_000

export class WsClient {
  private ws: WebSocket | null = null
  private url: string
  private connected = false
  private connecting = false
  private closed = false
  private pendingRequests = new Map<string, PendingRequest>()
  private eventHandlers = new Map<string, Set<EventHandler>>()
  private connectionHandlers = new Set<(c: boolean) => void>()
  private queuedCalls: Array<() => void> = []
  private reconnectDelay = RECONNECT_BASE
  private connectReqId: string | null = null

  constructor(url: string) {
    this.url = url
  }

  connect(): void {
    if (this.closed || this.connecting || this.connected) return
    this.connecting = true
    this.ws = new WebSocket(this.url)
    this.ws.onopen = () => this.onOpen()
    this.ws.onmessage = (e) => this.onMessage(e.data as string)
    this.ws.onclose = (e) => this.onClose(e)
    this.ws.onerror = () => console.warn('[ws] socket error')
  }

  private onOpen(): void {
    this.connecting = false
    const id = crypto.randomUUID()
    this.connectReqId = id
    this.sendRaw({
      type: 'req', id,
      method: 'connect',
      params: { user_id: 'desktop', sender_id: 'desktop', protocol_version: 3 },
    })
  }

  private onMessage(data: string): void {
    let frame: Frame
    try { frame = JSON.parse(data) as Frame } catch { return }

    if (frame.type === 'res') {
      if (this.connectReqId && frame.id === this.connectReqId) {
        this.connectReqId = null
        if (frame.ok) {
          this.connected = true
          this.reconnectDelay = RECONNECT_BASE
          this.connectionHandlers.forEach((h) => h(true))
          const q = this.queuedCalls.splice(0)
          q.forEach((fn) => fn())
        }
        return
      }
      const p = this.pendingRequests.get(frame.id)
      if (!p) return
      clearTimeout(p.timer)
      this.pendingRequests.delete(frame.id)
      frame.ok ? p.resolve(frame.payload) : p.reject(
        Object.assign(new Error(frame.error?.message ?? 'RPC error'), { code: frame.error?.code })
      )
    } else if (frame.type === 'event') {
      console.log('[ws:event]', frame.event, frame.payload)
      this.eventHandlers.get(frame.event)?.forEach((h) => {
        try { h(frame.payload) } catch (e) { console.error('[ws] handler error', e) }
      })
    }
  }

  private onClose(e: CloseEvent): void {
    const was = this.connected
    this.connected = this.connecting = false
    this.ws = null
    if (was) this.connectionHandlers.forEach((h) => h(false))
    this.pendingRequests.forEach((p) => { clearTimeout(p.timer); p.reject(new Error('disconnected')) })
    this.pendingRequests.clear()
    if (!this.closed) {
      console.info(`[ws] reconnecting in ${this.reconnectDelay}ms (code=${e.code})`)
      setTimeout(() => {
        this.reconnectDelay = Math.min(this.reconnectDelay * 2, RECONNECT_MAX)
        this.connect()
      }, this.reconnectDelay)
    }
  }

  call<T = unknown>(method: string, params?: Record<string, unknown>): Promise<T> {
    const timeout = method.startsWith('chat.') ? CHAT_TIMEOUT : DEFAULT_TIMEOUT
    if (!this.connected) {
      return new Promise((resolve, reject) => {
        this.queuedCalls.push(() => this.call<T>(method, params).then(resolve as never, reject))
      })
    }
    return new Promise((resolve, reject) => {
      const id = crypto.randomUUID()
      const timer = setTimeout(() => {
        this.pendingRequests.delete(id)
        reject(new Error(`RPC timeout: ${method}`))
      }, timeout)
      this.pendingRequests.set(id, { resolve: resolve as (v: unknown) => void, reject, timer })
      this.sendRaw({ type: 'req', id, method, params })
    })
  }

  on(event: string, handler: EventHandler): () => void {
    if (!this.eventHandlers.has(event)) this.eventHandlers.set(event, new Set())
    this.eventHandlers.get(event)!.add(handler)
    return () => {
      this.eventHandlers.get(event)?.delete(handler)
    }
  }

  onConnectionChange(h: (c: boolean) => void): () => void {
    this.connectionHandlers.add(h)
    return () => this.connectionHandlers.delete(h)
  }

  close(): void {
    this.closed = true
    this.ws?.close()
  }

  private sendRaw(f: Frame): void {
    this.ws?.send(JSON.stringify(f))
  }

  get isConnected() { return this.connected }
}

let _client: WsClient | null = null

export function initWs(url: string): WsClient {
  _client?.close()
  _client = new WsClient(url)
  _client.connect()
  return _client
}

export function getWs(): WsClient {
  if (!_client) throw new Error('WsClient not initialized')
  return _client
}
