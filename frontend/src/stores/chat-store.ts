import { create } from 'zustand'
import type { Message, ToolCall } from '../types'

interface ChatState {
  messages: Message[]
  isRunning: boolean
  activity: { phase: string; tool?: string; iteration?: number } | null
  currentRunId: string | null   // Active assistant streaming message id; must be cleared when run ends

  // actions
  addUserMessage: (text: string, files?: string[]) => void
  addAssistantMessage: (runId: string) => void
  appendChunk: (text: string, reasoning?: string) => void
  finalizeMessage: (content: string, media?: Message['media']) => void
  addToolCall: (tc: Omit<ToolCall, 'id'> & { toolId: string; toolKey?: string }) => void
  updateToolResult: (toolId: string, result: string, error?: string, toolKey?: string) => void
  appendError: (msg: string) => void
  setMessages: (msgs: Message[]) => void
  setActivity: (a: { phase: string; tool?: string; iteration?: number } | null) => void
  startRun: () => void
  endRun: () => void
  clear: () => void
}

export const useChatStore = create<ChatState>()((set, get) => ({
  messages: [],
  isRunning: false,
  activity: null,
  currentRunId: null,

  addUserMessage: (text, files) => set((s) => ({
    messages: [...s.messages, {
      id: crypto.randomUUID(), session_id: '', role: 'user',
      content: text, files, created_at: Date.now(),
    }],
  })),

  addAssistantMessage: (runId) => {
    set((s) => ({
      currentRunId: runId,
      messages: [...s.messages, {
        id: runId, session_id: '', role: 'assistant',
        content: '', isStreaming: true, created_at: Date.now(),
      }],
    }))
  },

  appendChunk: (text, reasoning) => set((s) => {
    let runId = s.currentRunId
    let msg = runId ? s.messages.find((m) => m.id === runId) : undefined

    // No active assistant streaming message (e.g. previous round was interrupted by a tool call,
    // or chunk arrived before agent.run.started). Create a fresh assistant message at the bottom
    // so multi-round think→act→observe→think output is appended in chronological order.
    if (!msg) {
      const newId = crypto.randomUUID()
      const newMsg: Message = {
        id: newId, session_id: '', role: 'assistant',
        content: '', isStreaming: true, created_at: Date.now(),
      }
      const nextMessages = [...s.messages, newMsg]
      runId = newId
      msg = newMsg
      // Apply the chunk on top of the freshly created message below.
      s = { ...s, messages: nextMessages, currentRunId: newId } as typeof s
    }

    // If this is a reasoning increment, append directly to thinkContent
    if (reasoning) {
      const prevThink = (msg as any).thinkContent ?? ''
      return {
        currentRunId: runId,
        messages: s.messages.map((m) =>
          m.id === runId
            ? { ...m, thinkContent: prevThink + reasoning, isThinking: true } as any
            : m
        ),
      }
    }

    // Accumulated content so far + new chunk; concatenate and parse as a whole
    const prevRaw = (msg as any).__rawContent ?? ''
    const raw = prevRaw + text

    // Parse <think>...</think> tags, supporting cross-chunk streaming (compatible with legacy model tag style)
    let thinkContent = ''
    let content = ''
    let isThinking = false

    const thinkOpen = '<think>'
    const thinkClose = '</think>'
    const openIdx = raw.indexOf(thinkOpen)

    if (openIdx === -1) {
      // No <think> tag; all content
      content = raw
    } else {
      const closeIdx = raw.indexOf(thinkClose, openIdx)
      if (closeIdx === -1) {
        // <think> arrived but </think> not yet — thinking in progress
        thinkContent = raw.slice(openIdx + thinkOpen.length)
        content = raw.slice(0, openIdx)
        isThinking = true
      } else {
        // Complete <think>...</think> block
        thinkContent = raw.slice(openIdx + thinkOpen.length, closeIdx)
        content = raw.slice(0, openIdx) + raw.slice(closeIdx + thinkClose.length)
      }
    }

    return {
      currentRunId: runId,
      messages: s.messages.map((m) =>
        m.id === runId
          ? { ...m, content, thinkContent, isThinking, __rawContent: raw } as any
          : m
      ),
    }
  }),

  finalizeMessage: (content, media) => set((s) => {
    const runId = s.currentRunId
    // No active streaming message: most likely the run already produced assistant text via
    // streaming chunks and the last message has been "sealed" by a tool call. In that case,
    // do NOT overwrite history with the accumulated `output` (which spans all think rounds and
    // would duplicate earlier rounds). Just clear the pointer.
    if (!runId) {
      // Even when there is no active streaming message we still want the
      // generated media to appear: append a fresh assistant message that
      // carries only media + caption. This is the to-image / to-video path.
      if (media && media.length > 0) {
        return {
          currentRunId: null,
          messages: [...s.messages, {
            id: crypto.randomUUID(), session_id: '', role: 'assistant',
            content: content || '', media, created_at: Date.now(),
          }],
        }
      }
      return { currentRunId: null }
    }

    const msg = s.messages.find((m) => m.id === runId)

    // If the streaming message already has content from `agent.chunk`, prefer it.
    // Only fall back to `output` if streaming produced nothing (e.g. non-streaming providers).
    const streamed = (msg?.content ?? '').trim()
    const useStreamed = streamed.length > 0

    const thinkOpen = '<think>'
    const thinkClose = '</think>'
    const openIdx = useStreamed ? -1 : content.indexOf(thinkOpen)
    let thinkContent: string | undefined
    let finalContent = useStreamed ? streamed : content

    if (openIdx !== -1) {
      const closeIdx = content.indexOf(thinkClose, openIdx)
      if (closeIdx !== -1) {
        thinkContent = content.slice(openIdx + thinkOpen.length, closeIdx)
        finalContent = content.slice(0, openIdx) + content.slice(closeIdx + thinkClose.length)
      } else {
        thinkContent = content.slice(openIdx + thinkOpen.length)
        finalContent = content.slice(0, openIdx)
      }
    }

    return {
      currentRunId: null,   // Run ended; clear pointer to prevent late events from polluting history
      messages: s.messages.map((m) => {
        if (m.id !== runId) return m
        return {
          ...m,
          content: finalContent.trim(),
          // Prefer <think> tag parsing result; otherwise keep accumulated streaming reasoning content
          thinkContent: thinkContent ?? m.thinkContent,
          isThinking: false,
          isStreaming: false,
          media: media && media.length > 0 ? media : m.media,
        }
      }),
    }
  }),

  addToolCall: ({ toolId, toolKey, name, arguments: args, argumentsRaw, status }) => set((s) => {
    // When a tool call arrives mid-run, "seal" the current streaming assistant message so that
    // the next round of `agent.chunk` (after observe→think) creates a new assistant message
    // appended at the bottom, preserving chronological order in the UI.
    const runId = s.currentRunId
    const sealedMessages = runId
      ? s.messages.map((m) =>
          m.id === runId ? { ...m, isStreaming: false } : m
        )
      : s.messages
    const nextStatus = status ?? 'streaming'
    const key = toolKey || toolId
    const existing = sealedMessages.some((m) =>
      m.role === 'tool' && m.tool_calls?.some((tc) => (tc.toolKey || tc.id) === key)
    )
    if (existing) {
      return {
        currentRunId: null,
        messages: sealedMessages.map((m) => {
          if (m.role !== 'tool') return m
          const toolCalls = m.tool_calls?.map((tc) =>
            (tc.toolKey || tc.id) === key
              ? {
                  ...tc,
                  id: toolId || tc.id,
                  toolKey: key,
                  name: name || tc.name,
                  arguments: args ?? tc.arguments,
                  argumentsRaw: argumentsRaw ?? tc.argumentsRaw,
                  status: nextStatus,
                }
              : tc
          )
          return toolCalls ? { ...m, tool_calls: toolCalls, tool_name: name || m.tool_name } : m
        }),
      }
    }
    return {
      currentRunId: null,
      messages: [...sealedMessages, {
        id: key, session_id: '', role: 'tool',
        tool_name: name,
        tool_calls: [{ id: toolId || key, toolKey: key, name, arguments: args, argumentsRaw, status: nextStatus }],
        created_at: Date.now(),
      }],
    }
  }),

  updateToolResult: (toolId, result, error, toolKey) => set((s) => {
    const key = toolKey || toolId
    return {
    messages: s.messages.map((m) =>
      m.id === key || m.id === toolId
        ? {
            ...m,
            tool_calls: m.tool_calls?.map((tc) =>
              (tc.toolKey || tc.id) === key || tc.id === toolId
                ? { ...tc, id: toolId || tc.id, toolKey: key, result, isError: !!error, status: 'done' }
                : tc
            ),
          }
        : m
    ),
    }
  }),

  appendError: (msg) => set((s) => {
    const runId = s.currentRunId
    // No active assistant message yet (e.g. chat.send failed before agent.run.started):
    // append a standalone assistant error message so the user sees the failure.
    if (!runId) {
      return {
        messages: [...s.messages, {
          id: crypto.randomUUID(), session_id: '', role: 'assistant',
          content: `⚠️ ${msg}`, isStreaming: false, created_at: Date.now(),
        }],
      }
    }
    return {
      messages: s.messages.map((m) =>
        m.id === runId ? { ...m, content: (m.content ?? '') + `\n\n⚠️ ${msg}`, isStreaming: false } : m
      ),
    }
  }),

  setMessages: (msgs) => set({ messages: msgs }),
  setActivity: (a) => set({ activity: a }),
  startRun: () => set({ isRunning: true, activity: { phase: 'thinking' } }),
  endRun: () => set({ isRunning: false, activity: null, currentRunId: null }),
  clear: () => set({ messages: [], isRunning: false, activity: null, currentRunId: null }),
}))
