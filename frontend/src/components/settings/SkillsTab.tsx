import { useState, useEffect } from 'react'
import { getWs } from '../../lib/ws'
import { SectionTitle, Card, Toggle, Row } from './shared-ui'
import type { Skill } from './types'

export function SkillsTab() {
  const ws = getWs()
  const [skills, setSkills] = useState<Skill[]>([])
  const [loading, setLoading] = useState(true)

  const load = () => {
    ws.call<{ skills: Skill[] }>('skills.list')
      .then((r) => setSkills(r.skills ?? []))
      .catch(() => {})
      .finally(() => setLoading(false))
  }
  useEffect(() => { load() }, [ws])

  const toggle = async (id: string, enabled: boolean) => {
    await ws.call('skills.toggle', { id, enabled: !enabled }).catch(() => {})
    load()
  }

  return (
    <div className="space-y-4">
      <SectionTitle>Skill 列表</SectionTitle>
      {loading ? <p className="text-sm text-text-muted">加载中…</p>
        : skills.length === 0 ? (
          <div className="text-center py-10">
            <p className="text-3xl mb-2">⚡</p>
            <p className="text-sm text-text-muted">暂无 Skill</p>
          </div>
        ) : (
          <Card>
            {skills.map((s) => (
              <Row key={s.id} label={s.name} desc={s.version ? `v${s.version}` : undefined}>
                <Toggle value={s.enabled} onChange={() => toggle(s.id, s.enabled)} />
              </Row>
            ))}
          </Card>
        )}
    </div>
  )
}
