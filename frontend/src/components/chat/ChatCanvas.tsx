import { useEffect, useRef, useCallback } from 'react'
import { useAgents } from '../../hooks/use-agents'
import { useSessions } from '../../hooks/use-sessions'
import { useProjects } from '../../hooks/use-projects'
import { useChat } from '../../hooks/use-chat'
import { useModelCapabilities } from '../../hooks/use-model-capabilities'
import { useUiStore } from '../../stores/ui-store'
import { MessageBubble } from './MessageBubble'
import { InputBar } from './InputBar'
import { Spinner } from '../ui'
import { AgentTagBadge } from '../agents/AgentTagBadge'
import { getToolLabel } from '../../lib/tool-labels'

// Phase label mapping
const PHASE_LABELS: Record<string, string> = {
  thinking: '思考中',
  acting: '执行工具',
  observing: '处理结果',
  memory: '整理记忆',
  summarizing: '生成摘要',
  retrying: '重试中',
}

// Tool display name mapping — centralized in lib/tool-labels.ts

function ActivityDot({ phase, tool }: { phase: string; tool?: string }) {
  const toolLabel = tool ? getToolLabel(tool) : undefined
  return (
    <div className="flex items-center gap-2 mb-3 px-1 animate-fade-in">
      <div className="w-7 h-7 rounded-lg bg-surface-tertiary border border-border flex items-center justify-center shrink-0">
        <span className="text-base">🤖</span>
      </div>
      <div className="flex items-center gap-2 bg-agent-bubble border border-border/60 rounded-2xl rounded-tl-sm px-3 py-2">
        <div className="flex gap-0.5 items-center">
          {[0, 1, 2].map((i) => (
            <div
              key={i}
              className="w-1.5 h-1.5 rounded-full bg-accent/60"
              style={{ animation: `pulse-soft 1.4s ease-in-out ${i * 0.2}s infinite` }}
            />
          ))}
        </div>
        <span className="text-xs text-text-muted">
          {toolLabel ? `调用 ${toolLabel}` : PHASE_LABELS[phase] ?? phase}
        </span>
      </div>
    </div>
  )
}

// Empty state — show current selected Agent info and suggestions
function EmptyState({ agentName, agentEmoji, onSuggestion }: {
  agentName?: string
  agentEmoji?: string
  onSuggestion?: (text: string) => void
}) {
  const SUGGESTIONS = [
    '你能做什么？',
    '帮我写一段代码',
    '解释一个概念',
    '给我一些建议',
  ]

  return (
    <div className="flex flex-col items-center justify-center text-center py-20 select-none">
      {agentEmoji ? (
        <div className="w-20 h-20 rounded-2xl bg-accent/10 border border-accent/20 flex items-center justify-center text-5xl mb-5 shadow-lg">
          {agentEmoji}
        </div>
      ) : (
        <div className="w-20 h-20 rounded-2xl bg-surface-tertiary border border-border flex items-center justify-center text-4xl mb-5">
          💬
        </div>
      )}
      <h2 className="text-lg font-semibold text-text-primary mb-1">
        {agentName ? `与 ${agentName} 对话` : '选择一个助手开始'}
      </h2>
      <p className="text-sm text-text-muted max-w-xs mb-6">
        {agentName
          ? '开始一段新对话，或从侧边栏选择历史会话'
          : '在左侧边栏选择一个 Agent，或创建新的助手'}
      </p>
      {agentName && (
        <div className="flex flex-wrap justify-center gap-2">
          {SUGGESTIONS.map((s) => (
            <button
              key={s}
              onClick={() => onSuggestion?.(s)}
              className="text-xs text-text-secondary bg-surface-secondary border border-border rounded-full px-3 py-1.5 hover:border-accent/40 hover:text-accent transition-colors"
            >
              {s}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

export function ChatCanvas() {
  const { agents, loading: agentsLoading } = useAgents()
  const activeAgentId = useUiStore((s) => s.activeAgentId)
  const activeSessionId = useUiStore((s) => s.activeSessionId)
  const activeProjectId = useUiStore((s) => s.activeProjectId)
  const setActiveSession = useUiStore((s) => s.setActiveSession)

  const agent = agents.find((a) => a.id === activeAgentId)
  const agentWithEmoji = agent as typeof agent & { emoji?: string } | undefined

  const { sessions } = useSessions(activeAgentId)
  const { projects } = useProjects()
  const activeProject = projects.find((p) => p.id === activeProjectId)
  const { messages, isRunning, activity, sendMessage, abort } = useChat()
  const { vision, loading: visionLoading } = useModelCapabilities(agent?.model)

  const scrollRef = useRef<HTMLDivElement>(null)
  const bottomRef = useRef<HTMLDivElement>(null)
  const userScrolled = useRef(false)

  // Auto-scroll to bottom when messages change
  useEffect(() => {
    if (!userScrolled.current) {
      bottomRef.current?.scrollIntoView({ behavior: isRunning ? 'smooth' : 'instant' })
    }
  }, [messages, isRunning])

  const handleScroll = useCallback(() => {
    const el = scrollRef.current
    if (!el) return
    userScrolled.current = el.scrollHeight - el.scrollTop - el.clientHeight > 50
  }, [])

  const handleSend = useCallback((text: string, files: string[] = []) => {
    if (!agent) return
    userScrolled.current = false
    sendMessage(text, agent.id, activeSessionId, (newSessionId) => {
      setActiveSession(newSessionId)
    }, activeProjectId, files)
  }, [agent, activeSessionId, activeProjectId, sendMessage, setActiveSession])

  const lastAssistantId = (() => {
    for (let i = messages.length - 1; i >= 0; i--) {
      if (messages[i].role === 'assistant') return messages[i].id
    }
    return null
  })()

  // After selecting an agent, auto-select the most recent session (skip during new-session to avoid overriding the "new" action)
  const resetKey = useUiStore((s) => s.sessionResetKey)
  const lastAutoSelectResetKey = useRef(-1)
  useEffect(() => {
    if (!activeAgentId || activeSessionId) return
    // resetKey changed means the user clicked "new", so don't auto-select a history session
    if (lastAutoSelectResetKey.current !== resetKey) {
      lastAutoSelectResetKey.current = resetKey
      return
    }
    if (sessions.length > 0) {
      setActiveSession(sessions[0].id)
    }
  }, [activeAgentId, sessions, activeSessionId, setActiveSession, resetKey])

  return (
    <div className="flex-1 flex flex-col min-h-0">
      {/* Top bar */}
      <div className="h-12 flex items-center justify-between px-4 shrink-0 bg-surface-primary/50 backdrop-blur-sm">
        <div className="flex items-center gap-2.5">
          {agent ? (
            <>
              <div className="w-7 h-7 rounded-lg bg-surface-tertiary border border-border flex items-center justify-center text-base">
                {agentWithEmoji?.emoji ?? '🤖'}
              </div>
              <div>
                <div className="flex items-center gap-1.5">
                  <p className="text-sm font-medium text-text-primary leading-tight">{agent.name}</p>
                  <AgentTagBadge tag={agent.tag} size="xs" />
                </div>
                <p className="text-2xs text-text-muted leading-tight">{agent.model}</p>
              </div>
              {activeProject && (
                <div className="flex items-center gap-1 ml-1 px-2 py-0.5 bg-accent/10 border border-accent/20 rounded-full">
                  <span className="text-2xs">📁</span>
                  <span className="text-2xs text-accent font-medium truncate max-w-24">{activeProject.name}</span>
                </div>
              )}
            </>
          ) : (
            <p className="text-sm text-text-muted">选择一个智能体开始对话</p>
          )}
        </div>

        {/* New chat */}
        {agent && (
          <button
            onClick={() => {
              setActiveSession(null)
              useUiStore.getState().incrementSessionResetKey()
            }}
            className="flex items-center gap-1.5 text-xs text-text-muted hover:text-text-primary hover:bg-surface-tertiary px-2.5 py-1.5 rounded-lg transition-colors"
            title="新对话 (⌘N)"
          >
            <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth={2.5} viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 4v16m8-8H4" />
            </svg>
            新对话
          </button>
        )}
      </div>

      {/* Messages + dots background */}
      <div className="flex-1 flex flex-col min-h-0 canvas-dots">
        <div
          ref={scrollRef}
          onScroll={handleScroll}
          className="flex-1 overflow-y-auto overscroll-contain px-4 py-4"
        >
          <div className="max-w-3xl mx-auto">
            {agentsLoading ? (
              <div className="flex items-center justify-center py-20">
                <Spinner size="md" />
              </div>
            ) : messages.length === 0 ? (
              <EmptyState
                agentName={agent?.name}
                agentEmoji={agentWithEmoji?.emoji}
                onSuggestion={handleSend}
              />
            ) : (
              messages.filter((msg) => msg.role !== 'assistant' || (msg.content ?? '').trim() !== '' || msg.thinkContent || msg.isThinking).map((msg) => (
                <MessageBubble
                  key={msg.id}
                  message={msg}
                  agentEmoji={agentWithEmoji?.emoji}
                  isStreaming={isRunning && msg.id === lastAssistantId}
                />
              ))
            )}

            {isRunning && activity && !(activity.phase === 'thinking' && messages.some((m) => {
              if (m.id !== lastAssistantId) return false
              // Hide the standalone "thinking" dot when the last assistant bubble
              // is already streaming any content (reasoning or final answer).
              return m.isThinking || !!m.thinkContent || (m.content ?? '').trim() !== ''
            })) && (
              <ActivityDot phase={activity.phase} tool={activity.tool} />
            )}

            <div ref={bottomRef} />
          </div>
        </div>

        <InputBar
          onSend={handleSend}
          onStop={abort}
          isRunning={isRunning}
          disabled={!agent}
          placeholder={agent ? `发消息给 ${agent.name}…` : '请先选择一个助手'}
          visionSupported={vision}
          visionLoading={visionLoading}
        />
      </div>
    </div>
  )
}
