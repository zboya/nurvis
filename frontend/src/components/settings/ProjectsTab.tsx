import { useState } from 'react'
import { useProjects } from '../../hooks/use-projects'
import { SectionTitle, Card } from './shared-ui'

export function ProjectsTab() {
  const { projects, loading, createProject, deleteProject, updateProject } = useProjects()
  const [showForm, setShowForm] = useState(false)
  const [editTarget, setEditTarget] = useState<typeof projects[0] | null>(null)
  const [form, setForm] = useState({ name: '', dir: '', description: '' })
  const [saving, setSaving] = useState(false)
  const [deleting, setDeleting] = useState<string | null>(null)
  const [deleteError, setDeleteError] = useState<string | null>(null)

  const openNew = () => { setForm({ name: '', dir: '', description: '' }); setEditTarget(null); setShowForm(true) }
  const openEdit = (p: typeof projects[0]) => {
    setForm({ name: p.name, dir: p.dir, description: p.description ?? '' })
    setEditTarget(p)
    setShowForm(true)
  }

  const handleSave = async () => {
    if (!form.name.trim() || !form.dir.trim()) return
    setSaving(true)
    if (editTarget) {
      await updateProject(editTarget.id, form.name, form.dir, form.description)
    } else {
      await createProject(form.name, form.dir, form.description)
    }
    setSaving(false)
    setShowForm(false)
  }

  const handleDelete = async (id: string) => {
    const target = projects.find((p) => p.id === id)
    if (!window.confirm(`确定要删除项目「${target?.name ?? ''}」吗？`)) return
    setDeleting(id)
    setDeleteError(null)
    try {
      await deleteProject(id)
    } catch (e: any) {
      setDeleteError(e?.message ?? '删除失败')
    }
    setDeleting(null)
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <SectionTitle>项目管理</SectionTitle>
        <button onClick={openNew}
          className="text-xs px-3 py-1.5 bg-accent/15 text-accent rounded-lg hover:bg-accent/25 transition-colors">
          + 添加项目
        </button>
      </div>

      {showForm && (
        <Card>
          <div className="p-4 space-y-3">
            <p className="text-xs font-medium text-text-primary">{editTarget ? '编辑项目' : '添加本地目录'}</p>
            <input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })}
              placeholder="项目名称" autoFocus
              className="w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent/60 transition-colors" />
            <input value={form.dir} onChange={(e) => setForm({ ...form, dir: e.target.value })}
              placeholder="本地目录路径，例：/Users/me/myproject"
              className="w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent/60 transition-colors font-mono" />
            <input value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })}
              placeholder="描述（可选）"
              className="w-full bg-surface-tertiary border border-border/60 rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent/60 transition-colors" />
            <div className="flex gap-2">
              <button onClick={handleSave} disabled={!form.name.trim() || !form.dir.trim() || saving}
                className="flex-1 py-2 bg-accent text-white text-sm font-medium rounded-lg disabled:opacity-40 hover:bg-accent-hover transition-colors">
                {saving ? '保存中…' : (editTarget ? '保存' : '添加')}
              </button>
              <button onClick={() => setShowForm(false)}
                className="flex-1 py-2 bg-surface-tertiary text-text-secondary text-sm rounded-lg hover:text-text-primary transition-colors">
                取消
              </button>
            </div>
          </div>
        </Card>
      )}

      {deleteError && (
        <p className="text-xs text-error mt-2">{deleteError}</p>
      )}
      {loading ? (
        <p className="text-sm text-text-muted">加载中…</p>
      ) : projects.length === 0 && !showForm ? (
        <div className="text-center py-10">
          <p className="text-3xl mb-2">📁</p>
          <p className="text-sm text-text-muted">暂无项目，点击「添加项目」关联本地目录</p>
          <p className="text-2xs text-text-muted mt-1">关联后可在对话时选择项目，工具将在该目录下运行</p>
        </div>
      ) : (
        <div className="space-y-2">
          {projects.map((p) => (
            <Card key={p.id}>
              <div className="flex items-center justify-between px-4 py-3">
                <div className="flex items-center gap-3 min-w-0">
                  <div className="w-9 h-9 rounded-xl bg-surface-tertiary flex items-center justify-center text-lg shrink-0">
                    📁
                  </div>
                  <div className="min-w-0">
                    <p className="text-sm font-medium text-text-primary">{p.name}</p>
                    <p className="text-2xs text-text-muted font-mono truncate">{p.dir}</p>
                    {p.description && <p className="text-2xs text-text-muted truncate">{p.description}</p>}
                  </div>
                </div>
                <div className="flex items-center gap-2 ml-3 shrink-0">
                  <button onClick={() => openEdit(p)}
                    className="text-xs px-2.5 py-1 bg-surface-tertiary text-text-secondary rounded-lg hover:text-text-primary transition-colors">
                    编辑
                  </button>
                  <button onClick={() => handleDelete(p.id)} disabled={deleting === p.id}
                    className="text-xs px-2.5 py-1 text-text-muted hover:text-error transition-colors disabled:opacity-40">
                    {deleting === p.id ? '…' : '删除'}
                  </button>
                </div>
              </div>
            </Card>
          ))}
        </div>
      )}
    </div>
  )
}
