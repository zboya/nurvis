import { useEffect, useCallback, useRef } from 'react'
import { getWs } from '../lib/ws'
import { useChatStore } from '../stores/chat-store'
import { useUiStore } from '../stores/ui-store'
import type { Message } from '../types'

export function useChat() {
  const ws = getWs()
  const {
    messages, isRunning, activity,
    addUserMessage, addAssistantMessage, appendChunk,
    finalizeMessage, addToolCall, updateToolResult,
    appendError, setMessages, setActivity, startRun, endRun, clear,
  } = useChatStore()

  const activeSessionId = useUiStore((s) => s.activeSessionId)
  const sessionRef = useRef(activeSessionId)
  sessionRef.current = activeSessionId

  // Flag: whether loadHistory is needed after run ends because session was created mid-run
  const pendingLoadAfterRun = useRef(false)
  // Flag: whether currently sending a new-session message (prevent clear on session creation)
  const sendingNewSession = useRef(false)
  // The session id of the currently running turn. Different from sessionRef (which mirrors the
  // selected session in UI store) — this one is updated as soon as we know the server-side
  // session id (either from chat.send response or agent.run.started), so that abort() works
  // even before activeSessionId has propagated through React state.
  const runningSessionRef = useRef<string | null>(null)
  // If user clicks stop before we have a session id, remember the intent and fire abort once known.
  const pendingAbortRef = useRef(false)

  const sendAbort = useCallback(async (sid: string) => {
    try { await ws.call('chat.abort', { session_id: sid }) } catch { /* ignore */ }
  }, [ws])

  const loadHistory = useCallback(async (sessionId: string) => {
    try {
      const res = await ws.call<{ messages: Message[] }>('chat.history', { session_id: sessionId })
      setMessages(res.messages ?? [])
    } catch { /* ignore */ }
  }, [ws, setMessages])

  // Subscribe to agent events
  useEffect(() => {
    const p = (raw: unknown) => (raw ?? {}) as Record<string, unknown>

    const unsubStarted = ws.on('agent.run.started', (raw) => {
      console.log('[WS] agent.run.started', raw)
      const d = p(raw)
      if (sessionRef.current && d['session_id'] !== sessionRef.current) return
      const sid = (d['session_id'] as string) ?? ''
      runningSessionRef.current = sid
      addAssistantMessage(sid)
      startRun()
      // If user clicked stop before session id was known, fire it now.
      if (pendingAbortRef.current && sid) {
        pendingAbortRef.current = false
        sendAbort(sid)
      }
    })
    const unsubChunk = ws.on('agent.chunk', (raw) => {
      console.log('[WS] agent.chunk', raw)
      const d = p(raw)
      if (sessionRef.current && d['session_id'] !== sessionRef.current) return
      appendChunk((d['content'] as string) ?? '', (d['reasoning'] as string) || undefined)
    })
    const unsubActivity = ws.on('agent.stage', (raw) => {
      console.log('[WS] agent.stage', raw)
      const d = p(raw)
      if (sessionRef.current && d['session_id'] !== sessionRef.current) return
      setActivity({
        phase: (d['phase'] as string) ?? 'thinking',
        tool: d['tool'] as string | undefined,
        iteration: d['iteration'] as number | undefined,
      })
    })
    const unsubToolCall = ws.on('tool.call', (raw) => {
      console.log('[WS] tool.call', raw)
      const d = p(raw)
      if (sessionRef.current && d['session_id'] !== sessionRef.current) return
      addToolCall({
        toolId: (d['id'] as string) ?? '',
        toolKey: d['tool_key'] as string | undefined,
        name: (d['name'] as string) ?? 'unknown',
        arguments: d['arguments'] ?? {},
        argumentsRaw: d['arguments_raw'] as string | undefined,
        status: d['status'] as 'streaming' | 'ready' | 'running' | 'done' | undefined,
      })
      if (d['status'] === 'running') {
        setActivity({ phase: 'acting', tool: d['name'] as string | undefined })
      }
    })
    const unsubToolResult = ws.on('tool.result', (raw) => {
      console.log('[WS] tool.result', raw)
      const d = p(raw)
      if (sessionRef.current && d['session_id'] !== sessionRef.current) return
      updateToolResult(
        (d['id'] as string) ?? '',
        (d['result'] as string) ?? '',
        d['is_error'] ? ((d['content'] as string) ?? 'Error') : undefined,
        d['tool_key'] as string | undefined,
      )
    })
    const unsubCompleted = ws.on('agent.run.completed', (raw) => {
      console.log('[WS] agent.run.completed', raw)
      const d = p(raw)
      if (sessionRef.current && d['session_id'] !== sessionRef.current) return
      finalizeMessage((d['output'] as string) ?? '', d['media'] as Message['media'])
      endRun()
      runningSessionRef.current = null
      pendingAbortRef.current = false
      // After run ends, if we skipped loadHistory due to session creation, load now
      if (pendingLoadAfterRun.current && sessionRef.current) {
        pendingLoadAfterRun.current = false
        loadHistory(sessionRef.current)
      }
    })
    const unsubFailed = ws.on('agent.run.failed', (raw) => {
      console.log('[WS] agent.run.failed', raw)
      const d = p(raw)
      if (sessionRef.current && d['session_id'] !== sessionRef.current) return
      appendError((d['error'] as string) ?? 'Unknown error')
      endRun()
      runningSessionRef.current = null
      pendingAbortRef.current = false
      if (pendingLoadAfterRun.current && sessionRef.current) {
        pendingLoadAfterRun.current = false
        loadHistory(sessionRef.current)
      }
    })
    const unsubAborted = ws.on('agent.run.aborted', (raw) => {
      console.log('[WS] agent.run.aborted', raw)
      const d = p(raw)
      if (sessionRef.current && d['session_id'] !== sessionRef.current) return
      endRun()
      runningSessionRef.current = null
      pendingAbortRef.current = false
      if (pendingLoadAfterRun.current && sessionRef.current) {
        pendingLoadAfterRun.current = false
        loadHistory(sessionRef.current)
      }
    })

    return () => {
      unsubStarted(); unsubChunk(); unsubActivity()
      unsubToolCall(); unsubToolResult()
      unsubCompleted(); unsubFailed(); unsubAborted()
    }
  }, [ws, addAssistantMessage, startRun, appendChunk, addToolCall, updateToolResult,
      setActivity, finalizeMessage, appendError, endRun, loadHistory, sendAbort])

  const sendMessage = useCallback(async (
    text: string,
    agentId: string,
    sessionId: string | null,
    onSessionCreated?: (sessionId: string) => void,
    projectId?: string | null,
    files?: string[],
  ) => {
    if (!text.trim() && (!files || files.length === 0)) return
    console.log('[Chat] sendMessage', { text, agentId, sessionId, projectId, files })
    addUserMessage(text || (files && files.length > 0 ? `[已附加 ${files.length} 个文件]` : ''), files)
    // Optimistically show "thinking" indicator immediately, before agent.run.started arrives,
    // to avoid a blank gap between user send and first server event.
    useChatStore.setState({ isRunning: true, activity: { phase: 'thinking' } })
    // Mark sending a new-session message to prevent onSessionCreated useEffect from clearing
    if (!sessionId) {
      sendingNewSession.current = true
    }
    try {
      const res = await ws.call<{ session_id: string }>('chat.send', {
        text,
        agent_id: agentId,
        ...(sessionId ? { session_id: sessionId } : {}),
        ...(projectId ? { project_id: projectId } : {}),
        ...(files && files.length > 0 ? { files } : {}),
        stream: true,
      })
      const sid = res?.session_id ?? sessionId ?? null
      if (sid) {
        runningSessionRef.current = sid
        // Stop was clicked before we had a session id — fire it now.
        if (pendingAbortRef.current) {
          pendingAbortRef.current = false
          sendAbort(sid)
        }
      }
      if (res?.session_id && !sessionId) {
        onSessionCreated?.(res.session_id)
      }
    } catch (e) {
      console.error('chat.send failed', e)
      sendingNewSession.current = false
      pendingAbortRef.current = false
      // Roll back the optimistic running state so the thinking indicator stops
      useChatStore.setState({ isRunning: false, activity: null })
      // Display the error in the chat stream so the user can see validation failures like invalid_attachment
      const msg = (e as Error)?.message ?? 'chat.send failed'
      appendError(msg)
    }
  }, [ws, addUserMessage, appendError, sendAbort])

  // Load history when switching sessions; when creating a new session activeSessionId stays null→null,
  // so use resetKey to force a clear each time the parent clicks "new"
  const prevSession = useRef<string | null>(null)
  const prevReset = useRef(0)
  const resetKey = useUiStore((s) => s.sessionResetKey)
  useEffect(() => {
    if (activeSessionId === prevSession.current && resetKey === prevReset.current) return
    console.log('[Chat] session changed', { activeSessionId, prevSession: prevSession.current, sendingNewSession: sendingNewSession.current, isRunning: useChatStore.getState().isRunning })
    prevSession.current = activeSessionId
    prevReset.current = resetKey

    // If session was created after sending a new-session message, skip clear and keep current messages
    if (sendingNewSession.current) {
      sendingNewSession.current = false
      pendingLoadAfterRun.current = true
      return
    }

    // If currently running (server created a session during new-session and called setActiveSession),
    // skip clear and wait for run to end before loadHistory, to avoid clearing streaming messages
    if (useChatStore.getState().isRunning) {
      pendingLoadAfterRun.current = true
      return
    }

    clear()
    if (activeSessionId) loadHistory(activeSessionId)
  }, [activeSessionId, resetKey, clear, loadHistory])

  const abort = useCallback(async () => {
    const sid = runningSessionRef.current ?? sessionRef.current
    if (!sid) {
      // Session id not known yet (chat.send still in flight). Remember the intent;
      // it will be fired as soon as session id is resolved.
      pendingAbortRef.current = true
      return
    }
    sendAbort(sid)
  }, [sendAbort])

  return { messages, isRunning, activity, sendMessage, abort }
}
