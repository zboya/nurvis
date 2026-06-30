export interface ModelInfo {
  name: string
  size_bytes?: number
  param_size?: string
  family?: string
  quant_level?: string
  format?: string
  modified_at?: string
  is_remote?: boolean
  context_len?: number
  capabilities?: string[]
}

export interface McpServer {
  id: string
  name: string
  transport: string
  command?: string
  url?: string
  enabled: boolean
}

export interface Skill {
  id: string
  name: string
  version?: string
  enabled: boolean
}

export interface Channel {
  id: string
  name: string
  type: string
  enabled: boolean
  agent_id?: string
  config?: Record<string, any>
}

export interface Credential {
  id: string
  name: string
  provider: string
  enabled: boolean
  has_config: boolean
}

export interface CronJob {
  id: string
  name: string
  spec: string
  agent_id: string
  project_id?: string
  prompt: string
  enabled: boolean
  target_channel_id?: string
  target_peer_id?: string
  target_peer_type?: string
}

export interface CronRun {
  id: string
  job_id: string
  session_id?: string
  status: string
  error?: string
  started_at: string
  finished_at?: string
}

export type ChannelType = 'qq' | 'wechat' | 'wework' | 'dingtalk'

export interface QQConfig {
  app_id: string
  app_secret: string
  sandbox?: boolean
}

export interface WeWorkConfig {
  corp_id: string
  corp_secret: string
  agent_id: number
  callback_port?: number
  callback_path?: string
}

export interface DingTalkConfig {
  webhook_url?: string
  secret?: string
  app_key?: string
  app_secret?: string
  robot_code?: string
  callback_port?: number
  callback_path?: string
}

export interface LibraryModel {
  id: string
  modelId?: string
  likes?: number
  downloads?: number
  trendingScore?: number
  tags?: string[]
  pipeline_tag?: string
}

export interface RepoFile {
  path: string
  size?: number
  type?: string
}
