import { useState, useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { getWs } from '../../lib/ws'
import { SelectFiles } from '../../../bindings/github.com/zboya/nurvis/cmd/nurvis-desktop/service'
import type { Agent, AgentInput } from '../../types'
import { Button, Input, Textarea } from '../ui'
import { getToolLabel } from '../../lib/tool-labels'

interface Props {
  agent?: Agent | null
  onSave: (agent: Agent) => void
  onCancel: () => void
}

const EMOJI_OPTIONS = ['🤖', '💻', '✍️', '🔍', '🎨', '📊', '🧪', '🎯', '🚀', '🦊', '🐙', '🦁']

interface FormData {
  name: string
  model: string
  system_prompt: string
  max_rounds: number
  context_window: number
  tag: 'to-text' | 'to-image' | 'to-video'
  chat_model: string
  vae_path: string
}

export function AgentFormDialog({ agent, onSave, onCancel }: Props) {
  const isEdit = !!agent
  const [selectedEmoji, setSelectedEmoji] = useState(
    (agent as (Agent & { emoji?: string }))?.emoji ?? '🤖'
  )
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  // Local model list
  const [localModels, setLocalModels] = useState<string[]>([])
  const [modelsLoading, setModelsLoading] = useState(false)

  useEffect(() => {
    setModelsLoading(true)
    getWs().call<{ models: { name: string }[] }>('models.list')
      .then((res) => setLocalModels((res.models ?? []).map((m) => m.name).sort()))
      .catch(() => setLocalModels([]))
      .finally(() => setModelsLoading(false))
  }, [])

  // Tool allowlist state
  const [allTools, setAllTools] = useState<string[]>([])
  const [selectedTools, setSelectedTools] = useState<Set<string>>(
    new Set(agent?.allowed_tools ?? [])
  )
  const [toolsLoading, setToolsLoading] = useState(false)

  useEffect(() => {
    setToolsLoading(true)
    getWs().call<{ names: string[] }>('tools.names')
      .then((res) => setAllTools((res.names ?? []).sort()))
      .catch(() => setAllTools([]))
      .finally(() => setToolsLoading(false))
  }, [])

  const toggleTool = (name: string) => {
    setSelectedTools((prev) => {
      const next = new Set(prev)
      if (next.has(name)) next.delete(name)
      else next.add(name)
      return next
    })
  }

  const toggleAll = () => {
    if (selectedTools.size === allTools.length) {
      setSelectedTools(new Set())
    } else {
      setSelectedTools(new Set(allTools))
    }
  }

  // Parse existing context_window from options_json (fallback 32k)
  const initialContextWindow = (() => {
    try {
      const opts = agent?.options_json ? JSON.parse(agent.options_json) : null
      const raw = opts?.context_window
      const n = typeof raw === 'number' ? raw : Number(raw)
      return Number.isFinite(n) && n > 0 ? n : 32768
    } catch {
      return 32768
    }
  })()

  // Parse existing vae path from options_json (optional)
  const initialVaePath = (() => {
    try {
      const opts = agent?.options_json ? JSON.parse(agent.options_json) : null
      const v = opts?.vae
      return typeof v === 'string' ? v : ''
    } catch {
      return ''
    }
  })()

  const { register, handleSubmit, watch, setValue, formState: { errors } } = useForm<FormData>({
    defaultValues: {
      name: agent?.name ?? '',
      model: agent?.model ?? 'gemma4:e4b',
      system_prompt: agent?.system_prompt ?? '',
      max_rounds: agent?.max_rounds ?? 16,
      context_window: initialContextWindow,
      tag: (agent?.tag as 'to-text' | 'to-image' | 'to-video') ?? 'to-text',
      chat_model: agent?.chat_model ?? '',
      vae_path: initialVaePath,
    },
  })

  const watchedTag = watch('tag')
  const isMediaTag = watchedTag === 'to-image' || watchedTag === 'to-video'
  const watchedModel = watch('model')
  const watchedChatModel = watch('chat_model')

  // Treat a value as a local file path when it doesn't match any registered
  // model name. In that case the field is rendered as a free-text Input so
  // the absolute path is preserved.
  const isCustomPath = (v: string | undefined) =>
    !!v && localModels.length > 0 && !localModels.includes(v)

  const pickLocalFile = async (
    field: 'model' | 'chat_model' | 'vae_path'
  ) => {
    try {
      const files = await SelectFiles()
      if (files && files.length > 0) {
        setValue(field, files[0], { shouldDirty: true, shouldValidate: true })
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : '选择文件失败')
    }
  }

  const onValid = async (data: FormData) => {
    setSaving(true)
    setError('')
    try {
      // Merge context_window into options (preserve other existing options)
      let existingOptions: Record<string, unknown> = {}
      try {
        existingOptions = agent?.options_json ? JSON.parse(agent.options_json) : {}
      } catch { existingOptions = {} }
      const ctxWin = Number(data.context_window)
      const mergedOptions: Record<string, unknown> = {
        ...existingOptions,
        context_window: Number.isFinite(ctxWin) && ctxWin > 0 ? Math.floor(ctxWin) : 32768,
      }
      const vaeTrimmed = (data.vae_path ?? '').trim()
      if (isMediaTag && vaeTrimmed) {
        mergedOptions.vae = vaeTrimmed
      } else {
        // Clear stale vae when switching away from media tag or emptied
        delete mergedOptions.vae
      }

      const input: AgentInput & { emoji?: string; options?: Record<string, unknown> } = {
        name: data.name.trim(),
        model: data.model.trim(),
        system_prompt: data.system_prompt.trim() || undefined,
        max_rounds: Number(data.max_rounds),
        enabled: true,
        emoji: selectedEmoji,
        // Empty set = no restriction (pass empty array)
        allowed_tools: selectedTools.size > 0 ? Array.from(selectedTools) : [],
        options: mergedOptions,
        tag: data.tag,
        chat_model: isMediaTag ? data.chat_model.trim() : undefined,
      }
      if (isMediaTag && !data.chat_model.trim()) {
        setError('to-image / to-video 助手必须选择对话模型')
        setSaving(false)
        return
      }
      let res: { agent: Agent }
      if (isEdit) {
        res = await getWs().call<{ agent: Agent }>('agents.update', { id: agent!.id, ...input })
      } else {
        res = await getWs().call<{ agent: Agent }>('agents.create', input as unknown as Record<string, unknown>)
      }
      onSave(res.agent)
    } catch (e) {
      setError(e instanceof Error ? e.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const allSelected = allTools.length > 0 && selectedTools.size === allTools.length
  const noneSelected = selectedTools.size === 0

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm">
      <div className="bg-surface-secondary border border-border rounded-2xl shadow-2xl w-full max-w-2xl mx-4 overflow-hidden animate-slide-up flex flex-col max-h-[90vh]">
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-border shrink-0">
          <h3 className="text-sm font-semibold text-text-primary">
            {isEdit ? '编辑助手' : '创建助手'}
          </h3>
          <button onClick={onCancel} className="p-1 text-text-muted hover:text-text-primary rounded-lg transition-colors">
            <svg className="w-4 h-4" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        {/* Body — scrollable */}
        <form onSubmit={handleSubmit(onValid)} className="p-5 space-y-4 overflow-y-auto flex-1">
          {/* Emoji picker */}
          <div className="space-y-2">
            <label className="block text-xs font-medium text-text-secondary">图标</label>
            <div className="flex flex-wrap gap-2">
              {EMOJI_OPTIONS.map((e) => (
                <button
                  key={e}
                  type="button"
                  onClick={() => setSelectedEmoji(e)}
                  className={[
                    'w-9 h-9 rounded-xl text-lg flex items-center justify-center transition-all',
                    selectedEmoji === e
                      ? 'bg-accent/20 border-2 border-accent'
                      : 'bg-surface-tertiary border border-border hover:border-accent/40',
                  ].join(' ')}
                >
                  {e}
                </button>
              ))}
            </div>
          </div>

          <Input
            label="名称"
            placeholder="我的助手"
            {...register('name', { required: '请输入名称' })}
            error={errors.name?.message}
          />

          <div className="space-y-1">
            <label className="block text-xs font-medium text-text-secondary">模态</label>
            <select
              className="w-full bg-surface-tertiary border border-border rounded-lg px-3 py-2 text-sm text-text-primary focus:outline-none focus:ring-1 focus:ring-accent"
              {...register('tag')}
            >
              <option value="to-text">to-text（文本对话）</option>
              <option value="to-image">to-image（生成图片）</option>
              <option value="to-video">to-video（生成视频）</option>
            </select>
            <p className="text-xs text-text-muted">
              to-image / to-video 通过 sd-server 调用扩散模型，需要额外指定一个对话模型。
            </p>
          </div>

          <div className="space-y-1">
            <label className="block text-xs font-medium text-text-secondary">
              {isMediaTag ? '扩散模型（diffusion）' : '模型'}
            </label>
            {modelsLoading ? (
              <div className="text-xs text-text-muted py-2">加载模型列表…</div>
            ) : localModels.length > 0 && !isCustomPath(watchedModel) ? (
              <div className="relative">
                <select
                  className="w-full bg-surface-tertiary border border-border rounded-lg pl-3 pr-10 py-2 text-sm text-text-primary focus:outline-none focus:ring-1 focus:ring-accent"
                  {...register('model', { required: '请选择模型' })}
                >
                  {localModels.map((m) => (
                    <option key={m} value={m}>{m}</option>
                  ))}
                </select>
                <FolderPickButton onClick={() => pickLocalFile('model')} />
              </div>
            ) : (
              <div className="relative">
                <Input
                  className="pr-10"
                  placeholder="gemma4:e4b 或 /absolute/path/to/model.gguf"
                  {...register('model', { required: '请填写模型' })}
                  error={errors.model?.message}
                />
                <FolderPickButton onClick={() => pickLocalFile('model')} />
              </div>
            )}
            {errors.model?.message && localModels.length > 0 && !isCustomPath(watchedModel) && (
              <p className="text-xs text-error">{errors.model.message}</p>
            )}
          </div>

          {isMediaTag && (
            <div className="space-y-1">
              <label className="block text-xs font-medium text-text-secondary">
                对话模型（chat model，必填）
              </label>
              {modelsLoading ? (
                <div className="text-xs text-text-muted py-2">加载模型列表…</div>
              ) : localModels.length > 0 && !isCustomPath(watchedChatModel) ? (
                <div className="relative">
                  <select
                    className="w-full bg-surface-tertiary border border-border rounded-lg pl-3 pr-10 py-2 text-sm text-text-primary focus:outline-none focus:ring-1 focus:ring-accent"
                    {...register('chat_model', { required: isMediaTag ? '请选择对话模型' : false })}
                  >
                    <option value="">请选择…</option>
                    {localModels.map((m) => (
                      <option key={m} value={m}>{m}</option>
                    ))}
                  </select>
                  <FolderPickButton onClick={() => pickLocalFile('chat_model')} />
                </div>
              ) : (
                <div className="relative">
                  <Input
                    className="pr-10"
                    placeholder="gemma4:e4b 或 /absolute/path/to/model.gguf"
                    {...register('chat_model', { required: isMediaTag ? '请填写对话模型' : false })}
                    error={errors.chat_model?.message}
                  />
                  <FolderPickButton onClick={() => pickLocalFile('chat_model')} />
                </div>
              )}
              {errors.chat_model?.message && (
                <p className="text-xs text-error">{errors.chat_model.message}</p>
              )}
              <p className="text-xs text-text-muted">
                负责与用户对话、润色 prompt、说明生成结果；不会用于实际的图片/视频推理。
              </p>
            </div>
          )}

          {isMediaTag && (
            <div className="space-y-1">
              <label className="block text-xs font-medium text-text-secondary">
                VAE 文件路径（可选）
              </label>
              <div className="relative">
                <Input
                  className="pr-10"
                  placeholder="/absolute/path/to/vae.safetensors"
                  {...register('vae_path')}
                />
                <FolderPickButton onClick={() => pickLocalFile('vae_path')} />
              </div>
              <p className="text-xs text-text-muted">
                为扩散模型指定外置 VAE 文件；留空则使用模型自带 VAE。
              </p>
            </div>
          )}

          <Textarea
            label="系统提示词"
            placeholder="描述这个助手的角色和行为…"
            rows={4}
            {...register('system_prompt')}
          />

          <div className="space-y-1">
            <label className="block text-xs font-medium text-text-secondary">
              最大工具调用轮次
            </label>
            <input
              type="number"
              min={1}
              max={64}
              className="w-full bg-surface-tertiary border border-border rounded-lg px-3 py-2 text-sm text-text-primary focus:outline-none focus:ring-1 focus:ring-accent"
              {...register('max_rounds', { valueAsNumber: true })}
            />
          </div>

          <div className="space-y-1">
            <label className="block text-xs font-medium text-text-secondary">
              上下文窗口长度（tokens）
              <span className="ml-1.5 text-text-muted font-normal">默认 32768（32k）</span>
            </label>
            <input
              type="number"
              min={1024}
              max={1048576}
              step={1024}
              className="w-full bg-surface-tertiary border border-border rounded-lg px-3 py-2 text-sm text-text-primary focus:outline-none focus:ring-1 focus:ring-accent"
              {...register('context_window', { valueAsNumber: true })}
            />
            <p className="text-xs text-text-muted">
              控制模型可见的最大上下文 tokens 数；超过部分会被裁剪/摘要。
            </p>
          </div>

          {/* Tool allowlist */}
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <label className="block text-xs font-medium text-text-secondary">
                允许使用的工具
                <span className="ml-1.5 text-text-muted font-normal">
                  {noneSelected ? '（不限制）' : `（${selectedTools.size} / ${allTools.length}）`}
                </span>
              </label>
              {!toolsLoading && allTools.length > 0 && (
                <button
                  type="button"
                  onClick={toggleAll}
                  className="text-xs text-accent hover:underline"
                >
                  {allSelected ? '取消全选' : '全选'}
                </button>
              )}
            </div>

            {toolsLoading ? (
              <div className="text-xs text-text-muted py-2">加载中…</div>
            ) : allTools.length === 0 ? (
              <div className="text-xs text-text-muted py-2">暂无可用工具</div>
            ) : (
              <div className="bg-surface-tertiary border border-border rounded-lg divide-y divide-border overflow-hidden">
                {allTools.map((name) => {
                  const checked = selectedTools.has(name)
                  const label = getToolLabel(name)
                  const showZh = label !== name
                  return (
                    <label
                      key={name}
                      className="flex items-center gap-3 px-3 py-2 cursor-pointer hover:bg-surface-secondary transition-colors"
                    >
                      <input
                        type="checkbox"
                        checked={checked}
                        onChange={() => toggleTool(name)}
                        className="w-3.5 h-3.5 rounded border-border accent-accent"
                      />
                      <span className="text-xs font-mono text-text-primary">{name}</span>
                      {showZh && (
                        <span className="text-xs text-text-muted">{label}</span>
                      )}
                    </label>
                  )
                })}
              </div>
            )}
            <p className="text-xs text-text-muted">
              不勾选任何工具时，助手可使用所有已启用的工具。
            </p>
          </div>

          {error && <p className="text-xs text-error">{error}</p>}

          <div className="flex items-center justify-end gap-2 pt-1">
            <Button type="button" variant="secondary" onClick={onCancel}>取消</Button>
            <Button type="submit" variant="primary" loading={saving}>
              {isEdit ? '保存' : '创建'}
            </Button>
          </div>
        </form>
      </div>
    </div>
  )
}

// FolderPickButton renders a small folder icon button absolutely positioned
// inside the right edge of a relative-positioned input/select container.
// The parent must be `position: relative` and the input/select must reserve
// padding on the right (pr-10) so text never slides under the icon.
function FolderPickButton({ onClick }: { onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      title="选择本地文件"
      className="absolute right-1.5 top-1/2 -translate-y-1/2 w-7 h-7 flex items-center justify-center rounded-md text-text-muted hover:text-text-primary hover:bg-surface-secondary transition-colors"
    >
      <svg className="w-4 h-4" fill="none" stroke="currentColor" strokeWidth={1.8} viewBox="0 0 24 24">
        <path
          strokeLinecap="round"
          strokeLinejoin="round"
          d="M3 7a2 2 0 0 1 2-2h3.6a2 2 0 0 1 1.4.6L11.4 7H19a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z"
        />
      </svg>
    </button>
  )
}
