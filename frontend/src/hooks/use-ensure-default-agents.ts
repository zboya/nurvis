// useEnsureDefaultAgents — guarantees the two built-in default agents
// (对话宝 / 生图宝) exist whenever the user is inside the main app.
//
// Why this exists:
//   The onboarding wizard already creates these agents after their models
//   finish downloading. But the user can now "skip" onboarding before any
//   download completes, landing in the main app with zero agents. This hook
//   picks up where onboarding left off: for any default agent that doesn't
//   exist yet, it polls models.list and, once the required model(s) are
//   present locally, calls agents.create. It also reflects the in-flight
//   state in the global ui-store's pendingAgents list so AppShell's banner
//   keeps showing "正在创建中…" until each agent is finally created.
//
// Idempotency:
//   The ensure*Agent helpers internally short-circuit when an agent with the
//   same name already exists, so running this hook multiple times — or
//   alongside onboarding — is safe.

import { useEffect } from 'react'
import { useAgents } from './use-agents'
import { useUiStore } from '../stores/ui-store'
import {
  CHAT_BOT_PRESET,
  IMAGE_BOT_PRESET,
  IMAGE_CHAT_REF,
  IMAGE_VAE_REF,
  IMAGE_DIFF_REF,
  ensureChatBotAgent,
  ensureImageBotAgent,
  modelInstalled,
  recommendedChatModelRef,
} from '../lib/default-agents'

// Module-level guards so that React Strict Mode double-invocations and remounts
// don't spawn duplicate polling loops for the same default agent.
const inFlight = new Set<'chat_bot' | 'image_bot'>()

export function useEnsureDefaultAgents() {
  const { agents, loading, load } = useAgents()
  const addPendingAgent = useUiStore((s) => s.addPendingAgent)
  const removePendingAgent = useUiStore((s) => s.removePendingAgent)

  useEffect(() => {
    if (loading) return

    let cancelled = false

    const hasChatBot = agents.some((a) => a.name === CHAT_BOT_PRESET.name)
    const hasImageBot = agents.some((a) => a.name === IMAGE_BOT_PRESET.name)

    if (!hasChatBot && !inFlight.has('chat_bot')) {
      inFlight.add('chat_bot')
      addPendingAgent({
        key: CHAT_BOT_PRESET.key,
        emoji: CHAT_BOT_PRESET.emoji,
        name: CHAT_BOT_PRESET.name,
        hint: '对话模型仍在下载，下载完成后将自动创建',
      })
      ;(async () => {
        try {
          const ref = await recommendedChatModelRef()
          // Poll up to 4 hours: enough headroom for slow networks while still
          // surrendering eventually so we don't leak the polling loop forever.
          const deadline = Date.now() + 4 * 60 * 60 * 1000
          while (!cancelled && Date.now() < deadline) {
            try {
              if (await modelInstalled(ref)) {
                const created = await ensureChatBotAgent(ref)
                if (created) {
                  // Refresh the agents list so sidebar / pickers pick it up.
                  await load()
                  removePendingAgent(CHAT_BOT_PRESET.key)
                }
                return
              }
            } catch {
              /* keep polling */
            }
            await new Promise((r) => setTimeout(r, 5000))
          }
          if (!cancelled) removePendingAgent(CHAT_BOT_PRESET.key)
        } finally {
          inFlight.delete('chat_bot')
        }
      })()
    }

    if (!hasImageBot && !inFlight.has('image_bot')) {
      inFlight.add('image_bot')
      addPendingAgent({
        key: IMAGE_BOT_PRESET.key,
        emoji: IMAGE_BOT_PRESET.emoji,
        name: IMAGE_BOT_PRESET.name,
        hint: '扩散模型与 VAE 仍在下载，完成后将自动创建',
      })
      ;(async () => {
        try {
          const needed = [IMAGE_CHAT_REF, IMAGE_VAE_REF, IMAGE_DIFF_REF]
          const deadline = Date.now() + 4 * 60 * 60 * 1000
          while (!cancelled && Date.now() < deadline) {
            try {
              const checks = await Promise.all(needed.map((n) => modelInstalled(n)))
              if (checks.every(Boolean)) {
                const created = await ensureImageBotAgent(IMAGE_CHAT_REF, IMAGE_VAE_REF, IMAGE_DIFF_REF)
                if (created) {
                  await load()
                  removePendingAgent(IMAGE_BOT_PRESET.key)
                }
                return
              }
            } catch {
              /* keep polling */
            }
            await new Promise((r) => setTimeout(r, 5000))
          }
          if (!cancelled) removePendingAgent(IMAGE_BOT_PRESET.key)
        } finally {
          inFlight.delete('image_bot')
        }
      })()
    }

    // Both already exist — make sure the banner isn't stuck showing them.
    if (hasChatBot) removePendingAgent(CHAT_BOT_PRESET.key)
    if (hasImageBot) removePendingAgent(IMAGE_BOT_PRESET.key)

    return () => {
      cancelled = true
    }
  }, [agents, loading, load, addPendingAgent, removePendingAgent])
}
