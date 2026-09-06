import type { SkillMapSkill } from '../api'

/** Lay out actual prerequisite edges, including older maps in arbitrary order. */
export function layoutSkillGraph(skills: SkillMapSkill[]) {
  const byId = new Map(skills.map((skill) => [skill.id, skill]))
  const depths = new Map<string, number>()
  const visiting = new Set<string>()
  const edges: { from: string; to: string }[] = []
  const depth = (id: string): number => {
    if (depths.has(id)) return depths.get(id)!
    visiting.add(id)
    let level = 0
    for (const parent of new Set(byId.get(id)?.prerequisites ?? [])) {
      if (!byId.has(parent) || visiting.has(parent)) continue
      level = Math.max(level, depth(parent) + 1)
      edges.push({ from: parent, to: id })
    }
    visiting.delete(id)
    depths.set(id, level)
    return level
  }
  skills.forEach((skill) => depth(skill.id))
  const rows = new Map<number, number>()
  const nodes = skills.map((skill, index) => {
    const column = depths.get(skill.id)!
    const row = rows.get(column) ?? 0
    rows.set(column, row + 1)
    return { skill, index, x: column * 200 + 24, y: row * 170 + 16 }
  })
  const positions = new Map(nodes.map((node) => [node.skill.id, node]))
  return {
    nodes,
    edges: edges.map((edge) => ({ ...edge, start: positions.get(edge.from)!, end: positions.get(edge.to)! })),
    width: Math.max(1, ...Array.from(depths.values(), (value) => value + 1)) * 200,
    height: Math.max(1, ...rows.values()) * 170 + 16,
  }
}
