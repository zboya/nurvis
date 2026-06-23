import { PreparingStep } from './PreparingStep'

interface Props {
  onComplete: () => void
}

// New onboarding flow: zero-touch setup.
// We no longer ask the user to pick a model or fill in an agent form. Instead:
//   1. detect hardware → pick a recommended chat model
//   2. auto-download that model + the three image-bot models (chat / vae / diffusion)
//   3. auto-create two default agents: 对话宝 (to-text) and 生图宝 (to-image)
// The user just waits for downloads to finish, then lands directly in the chat UI.
export function OnboardingWizard({ onComplete }: Props) {
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

        {/* Card */}
        <div className="bg-surface-secondary/90 backdrop-blur-xl border border-white/10 rounded-2xl shadow-2xl overflow-hidden">
          <div className="p-6 pb-4">
            <h2 className="text-base font-semibold text-text-primary">首次启动准备</h2>
            <p className="text-xs text-text-muted mt-0.5">
              首次启动会自动下载默认助手所需的模型，全程无需操作
            </p>
          </div>
          <div className="px-6 pb-6 overflow-y-auto max-h-[70vh]">
            <PreparingStep onComplete={onComplete} />
          </div>
        </div>

        <p className="text-center text-xs text-white/30">
          Nurvis · 本地 AI 运行时 · 数据仅存于本机
        </p>
      </div>
    </div>
  )
}
