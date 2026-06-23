// Shared definitions and helpers for Nurvis' two built-in default agents
// (对话宝 / 生图宝). These are used both by the onboarding wizard and by the
// in-app "auto-seed" guard that creates them when the user lands in the main
// app with zero agents (e.g. after skipping onboarding before downloads
// finished).
//
// Keep all model refs, prompts and ensure*Agent helpers in one place so the
// onboarding and the in-app code paths can't drift.

import { getWs } from './ws'
import type { Agent, ModelRecommend } from '../types'

// Fixed model refs for the image-bot. Kept in sync with the onboarding flow.
export const IMAGE_BOT_CHAT_REPO = 'unsloth/Qwen3-4B-Instruct-2507-GGUF'
export const IMAGE_BOT_CHAT_FILE = 'Qwen3-4B-Instruct-2507-Q4_K_M.gguf'
export const IMAGE_BOT_VAE_REPO = 'ffxvs/vae-flux'
export const IMAGE_BOT_VAE_FILE = 'ae.safetensors'
export const IMAGE_BOT_DIFFUSION_REPO = 'leejet/Z-Image-Turbo-GGUF'
export const IMAGE_BOT_DIFFUSION_FILE = 'z_image_turbo-Q6_K.gguf'

export const IMAGE_CHAT_REF = `${IMAGE_BOT_CHAT_REPO}/${IMAGE_BOT_CHAT_FILE}`
export const IMAGE_VAE_REF = `${IMAGE_BOT_VAE_REPO}/${IMAGE_BOT_VAE_FILE}`
export const IMAGE_DIFF_REF = `${IMAGE_BOT_DIFFUSION_REPO}/${IMAGE_BOT_DIFFUSION_FILE}`

export const CHAT_BOT_PRESET = {
  key: 'chat_bot' as const,
  emoji: '🤖',
  name: '对话宝',
  role: '通用助手',
  prompt: '你是一个聪明、友好、乐于助人的AI助手。',
}

export const IMAGE_BOT_PRESET = {
  key: 'image_bot' as const,
  emoji: '🎨',
  name: '生图宝',
  role: '图像创作助手',
  prompt: '你是一位富有创造力的图像创作助手，根据用户描述生成图片。',
}

// modelInstalled checks whether a given "repo/file" reference is present in
// the local model registry. The gateway's `models.list` returns rows whose
// `name` field is the file basename, so we match by repo+file primarily and
// fall back to file-name matching for legacy rows.
export async function modelInstalled(ref: string): Promise<boolean> {
  const file = ref.split('/').pop() ?? ref
  const res = await getWs().call<{ models?: Array<{ name?: string; repo?: string; file?: string }> }>(
    'models.list',
  )
  for (const m of res.models ?? []) {
    const repoFile = m.repo && m.file ? `${m.repo}/${m.file}` : ''
    if (repoFile === ref) return true
    if (!m.repo && (m.name === file || m.file === file)) return true
    if (m.name === file && m.file === file) return true
  }
  return false
}

export async function findAgentByName(name: string): Promise<Agent | null> {
  try {
    const res = await getWs().call<{ agents?: Agent[] }>('agents.list')
    return (res.agents ?? []).find((a) => a.name === name) ?? null
  } catch {
    return null
  }
}

export async function ensureChatBotAgent(chatModelRef: string): Promise<Agent | null> {
  const existing = await findAgentByName(CHAT_BOT_PRESET.name)
  if (existing) return existing
  try {
    const res = await getWs().call<{ agent: Agent }>('agents.create', {
      name: CHAT_BOT_PRESET.name,
      role: CHAT_BOT_PRESET.role,
      system_prompt: CHAT_BOT_PRESET.prompt,
      model: chatModelRef,
      max_rounds: 16,
      enabled: true,
      tag: 'to-text',
      allowed_tools: [],
    })
    return res.agent
  } catch (e) {
    console.warn('[default-agents] create 对话宝 failed', e)
    return null
  }
}

export async function ensureImageBotAgent(
  imageChatRef: string,
  imageVaeRef: string,
  imageDiffRef: string,
): Promise<Agent | null> {
  const existing = await findAgentByName(IMAGE_BOT_PRESET.name)
  if (existing) return existing
  const imageOptions = {
    diffusion_model: imageDiffRef,
    vae: imageVaeRef,
  }
  try {
    const res = await getWs().call<{ agent: Agent }>('agents.create', {
      name: IMAGE_BOT_PRESET.name,
      role: IMAGE_BOT_PRESET.role,
      system_prompt: IMAGE_BOT_PRESET.prompt,
      model: imageChatRef,
      max_rounds: 8,
      enabled: true,
      tag: 'to-image',
      allowed_tools: [],
      options: imageOptions,
    } as Record<string, unknown>)
    return res.agent
  } catch (e) {
    console.warn('[default-agents] create 生图宝 failed', e)
    return null
  }
}

// Ask the gateway for the recommended chat-bot model ref. Falls back to the
// 1B gemma when the recommendation endpoint is unreachable.
export async function recommendedChatModelRef(): Promise<string> {
  try {
    const rec = await getWs().call<ModelRecommend>('models.recommend')
    return (
      rec.default_model ||
      rec.recommended?.[0] ||
      'ggml-org/gemma-3-1b-it-GGUF/gemma-3-1b-it-Q4_K_M.gguf'
    )
  } catch {
    return 'ggml-org/gemma-3-1b-it-GGUF/gemma-3-1b-it-Q4_K_M.gguf'
  }
}
