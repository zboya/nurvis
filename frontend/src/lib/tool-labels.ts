// Centralized tool i18n mapping.
//
// All UI surfaces that need to display a human-readable Chinese name for a
// tool key MUST import from this module. Add new tools here, not in feature
// components, to keep labels consistent across the app.

export const TOOL_LABELS: Record<string, string> = {
  // File system
  read_file: '读取文件',
  write_file: '写入文件',
  edit_file: '编辑文件',
  list_files: '列出文件',
  delete_file: '删除文件',
  glob: '查找文件',
  grep: '搜索内容',

  // Shell / process
  exec: '执行命令',

  // Web / network
  web_fetch: 'HTTP 请求',
  web_preview: '网页预览',

  // Channel / messaging
  'channel.send': '发送消息',

  // Scheduling
  cron: '定时任务',

  // Skills
  use_skill: '调用技能',

  // Deployment
  publish_cloudflare_pages: '部署到 Cloudflare Pages',
}

/**
 * Resolve a localized display name for a tool key.
 * Falls back to the original key when no mapping exists.
 */
export function getToolLabel(name: string | undefined | null): string {
  if (!name) return ''
  return TOOL_LABELS[name] ?? name
}
