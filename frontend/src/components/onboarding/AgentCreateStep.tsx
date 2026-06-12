import { useState, useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { getWs } from '../../lib/ws'
import type { Agent } from '../../types'
import { Button, Input, Textarea } from '../ui'

interface Props {
  defaultModel?: string
  onComplete: (agent: Agent) => void
}

// Preset roles
const PRESETS = [
  { emoji: '🤖', name: '通用助手', role: '通用助手', prompt: '你是一个聪明、友好、乐于助人的AI助手。' },
  { emoji: '💻', name: '编程助手', role: '编程专家', prompt: '你是一位资深软件工程师，擅长代码审查、调试、架构设计，回答简洁精准。' },
  { emoji: '✍️', name: '写作助手', role: '写作专家', prompt: '你是一位经验丰富的写作专家，擅长文章润色、创意写作和内容优化。' },
  { emoji: '🔍', name: '研究助手', role: '研究分析师', prompt: '你是一位严谨的研究分析师，善于收集信息、整理分析、提供洞见。' },
]

interface FormData {
  name: string
  model: string
  system_prompt: string
}

export function AgentCreateStep({ defaultModel = '', onComplete }: Props) {
  const [selectedPreset, setSelectedPreset] = useState<typeof PRESETS[0] | null>(null)
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState('')

  // Tool allowlist
  const [allTools, setAllTools] = useState<string[]>([])
  const [selectedTools, setSelectedTools] = useState<Set<string>>(new Set())
  const [toolsLoading, setToolsLoading] = useState(false)
  const [toolsExpanded, setToolsExpanded] = useState(false)

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
    if (selectedTools.size === allTools.length) setSelectedTools(new Set())
    else setSelectedTools(new Set(allTools))
  }

  const { register, handleSubmit, setValue, watch, formState: { errors } } = useForm<FormData>({
    defaultValues: { name: '', model: defaultModel, system_prompt: '' },
  })

  function pickPreset(p: typeof PRESETS[0]) {
    setSelectedPreset(p)
    if (!watch('name')) setValue('name', p.name)
    setValue('system_prompt', p.prompt)
  }

  const onValid = async (data: FormData) => {
    setCreating(true)
    setError('')
    try {
      const res = await getWs().call<{ agent: Agent }>('agents.create', {
        name: data.name.trim(),
        role: selectedPreset?.role ?? '通用助手',
        system_prompt: data.system_prompt.trim() || undefined,
        model: data.model.trim() || defaultModel || 'gemma3:4b',
        max_rounds: 16,
        enabled: true,
        allowed_tools: selectedTools.size > 0 ? Array.from(selectedTools) : [],
      })
      onComplete(res.agent)
    } catch (e) {
      setError(e instanceof Error ? e.message : '创建失败')
    } finally {
      setCreating(false)
    }
  }

  const allSelected = allTools.length > 0 && selectedTools.size === allTools.length
  const noneSelected = selectedTools.size === 0

  return (
    <form onSubmit={handleSubmit(onValid)} className="space-y-5 animate-slide-up">
      {/* Preset selection */}
      <div className="space-y-2">
        <p className="text-xs font-medium text-text-secondary">选择角色预设</p>
        <div className="grid grid-cols-2 gap-2">
          {PRESETS.map((p) => (
            <button
              key={p.name}
              type="button"
              onClick={() => pickPreset(p)}
              className={[
                'flex items-center gap-2.5 p-3 rounded-xl border text-left transition-all',
                selectedPreset?.name === p.name
                  ? 'border-accent bg-accent/10'
                  : 'border-border bg-surface-tertiary/30 hover:border-accent/30',
              ].join(' ')}
            >
              <span className="text-xl">{p.emoji}</span>
              <div>
                <p className="text-xs font-medium text-text-primary">{p.name}</p>
                <p className="text-2xs text-text-muted mt-0.5 line-clamp-1">{p.role}</p>
              </div>
            </button>
          ))}
        </div>
      </div>

      {/* Basic info */}
      <div className="space-y-3">
        <Input
          label="助手名称"
          placeholder={selectedPreset?.name ?? '我的助手'}
          {...register('name', { required: '请输入名称' })}
          error={errors.name?.message}
        />
        <Input
          label="模型"
          placeholder={defaultModel || 'gemma3:4b'}
          {...register('model')}
        />
        <Textarea
          label="系统提示词"
          placeholder="描述这个助手的角色和行为…"
          rows={3}
          {...register('system_prompt')}
        />
      </div>

      {/* Tool allowlist (collapsible) */}
      <div className="space-y-2">
        <button
          type="button"
          onClick={() => setToolsExpanded((v) => !v)}
          className="w-full flex items-center justify-between text-xs font-medium text-text-secondary hover:text-text-primary transition-colors"
        >
          <span className="flex items-center gap-1.5">
            <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" d="M11.42 15.17L17.25 21A2.652 2.652 0 0021 17.25l-5.877-5.877M11.42 15.17l2.496-3.03c.317-.384.74-.626 1.208-.766M11.42 15.17l-4.655 5.653a2.548 2.548 0 11-3.586-3.586l6.837-5.63m5.108-.233c.55-.164 1.163-.188 1.743-.14a4.5 4.5 0 004.486-6.336l-3.276 3.277a3.004 3.004 0 01-2.25-2.25l3.276-3.276a4.5 4.5 0 00-6.336 4.486c.091 1.076-.071 2.264-.904 2.95l-.102.085m-1.745 1.437L5.909 7.5H4.5L2.25 3.75l1.5-1.5L7.5 4.5v1.409l4.26 4.26m-1.745 1.437l1.745-1.437m6.615 8.206L15.75 15.75M4.867 19.125h.008v.008h-.008v-.008z" />
            </svg>
            允许使用的工具
            <span className="text-text-muted font-normal">
              {noneSelected ? '（不限制）' : `（已选 ${selectedTools.size} / ${allTools.length}）`}
            </span>
          </span>
          <svg
            className={['w-3.5 h-3.5 transition-transform', toolsExpanded ? 'rotate-180' : ''].join(' ')}
            fill="none" stroke="currentColor" strokeWidth={2} viewBox="0 0 24 24"
          >
            <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
          </svg>
        </button>

        {toolsExpanded && (
          <div className="space-y-2">
            {toolsLoading ? (
              <p className="text-xs text-text-muted py-1">加载中…</p>
            ) : allTools.length === 0 ? (
              <p className="text-xs text-text-muted py-1">暂无可用工具</p>
            ) : (
              <>
                <div className="flex justify-end">
                  <button type="button" onClick={toggleAll} className="text-xs text-accent hover:underline">
                    {allSelected ? '取消全选' : '全选'}
                  </button>
                </div>
                <div className="bg-surface-tertiary border border-border rounded-lg divide-y divide-border overflow-hidden max-h-44 overflow-y-auto">
                  {allTools.map((name) => (
                    <label
                      key={name}
                      className="flex items-center gap-3 px-3 py-2 cursor-pointer hover:bg-surface-secondary transition-colors"
                    >
                      <input
                        type="checkbox"
                        checked={selectedTools.has(name)}
                        onChange={() => toggleTool(name)}
                        className="w-3.5 h-3.5 rounded border-border accent-accent"
                      />
                      <span className="text-xs font-mono text-text-primary">{name}</span>
                    </label>
                  ))}
                </div>
              </>
            )}
            <p className="text-xs text-text-muted">不勾选任何工具时，助手可使用所有已启用的工具。</p>
          </div>
        )}
      </div>

      {error && <p className="text-xs text-error">{error}</p>}

      <div className="flex justify-end">
        <Button type="submit" variant="primary" size="md" loading={creating}>
          {creating ? '创建中…' : '创建助手 →'}
        </Button>
      </div>
    </form>
  )
}
