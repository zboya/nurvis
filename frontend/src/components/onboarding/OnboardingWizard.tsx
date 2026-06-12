import { useState } from 'react'
import { SetupStep } from './SetupStep'
import { AgentCreateStep } from './AgentCreateStep'
import type { Agent } from '../../types'

interface Props {
  onComplete: () => void
}

type Step = 'setup' | 'agent'

// Stepper step indicator
function Stepper({ current }: { current: Step }) {
  const steps: { key: Step; label: string }[] = [
    { key: 'setup', label: '初始化模型' },
    { key: 'agent', label: '创建助手' },
  ]
  const idx = steps.findIndex((s) => s.key === current)

  return (
    <div className="flex items-center gap-3 justify-center">
      {steps.map((s, i) => (
        <div key={s.key} className="flex items-center gap-3">
          <div className="flex items-center gap-2">
            <div className={[
              'w-6 h-6 rounded-full flex items-center justify-center text-xs font-bold transition-all',
              i < idx ? 'bg-success text-white' :
              i === idx ? 'bg-accent text-white' :
              'bg-surface-tertiary text-text-muted border border-border',
            ].join(' ')}>
              {i < idx ? (
                <svg className="w-3 h-3" fill="none" stroke="currentColor" strokeWidth={3} viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
                </svg>
              ) : i + 1}
            </div>
            <span className={[
              'text-xs font-medium',
              i === idx ? 'text-text-primary' : 'text-text-muted',
            ].join(' ')}>
              {s.label}
            </span>
          </div>
          {i < steps.length - 1 && (
            <div className={['h-px w-8 transition-colors', i < idx ? 'bg-success' : 'bg-border'].join(' ')} />
          )}
        </div>
      ))}
    </div>
  )
}

export function OnboardingWizard({ onComplete }: Props) {
  const [step, setStep] = useState<Step>('setup')
  const [setupModel, setSetupModel] = useState<string>('')

  const handleSetupDone = (model: string) => {
    setSetupModel(model)
    setStep('agent')
  }
  const handleAgentCreated = (_agent: Agent) => onComplete()

  return (
    <div className="min-h-dvh flex flex-col items-center justify-center app-bg px-4 py-8 overflow-y-auto">
      <div className="w-full max-w-3xl space-y-8 animate-fade-in my-auto">
        {/* Title */}
        <div className="text-center space-y-3">
          <div>
            <h1 className="text-3xl font-bold text-white tracking-tight">欢迎使用 Nurvis</h1>
            <p className="text-sm text-white/60 mt-1">本地优先的 AI Agent 平台，数据不离开你的电脑</p>
          </div>
        </div>

        <Stepper current={step} />

        {/* Card */}
        <div className="bg-surface-secondary/90 backdrop-blur-xl border border-white/10 rounded-2xl shadow-2xl overflow-hidden">
          <div className="p-6 pb-4">
            <h2 className="text-base font-semibold text-text-primary">
              {step === 'setup' ? '下载推荐模型' : '创建你的第一个助手'}
            </h2>
            <p className="text-xs text-text-muted mt-0.5">
              {step === 'setup'
                ? '根据你的硬件配置推荐最佳模型，支持本地私密运行'
                : '配置 AI 助手的角色、模型和行为'}
            </p>
          </div>
          <div className="px-6 pb-6 overflow-y-auto max-h-[60vh]">
            {step === 'setup' && <SetupStep onComplete={handleSetupDone} />}
            {step === 'agent' && <AgentCreateStep defaultModel={setupModel} onComplete={handleAgentCreated} />}
          </div>
        </div>

        <p className="text-center text-xs text-white/30">
          Nurvis · 本地 AI 运行时 · 数据仅存于本机
        </p>
      </div>
    </div>
  )
}
