import { test, expect } from '@playwright/test'
import { layoutSkillGraph } from '../src/study/skillGraph'

test('dependency layout preserves branches, joins and forward references', () => {
  const graph = layoutSkillGraph([
    { id: 'd', label: 'Apply', prerequisites: ['b', 'c'] },
    { id: 'b', label: 'Branch B', prerequisites: ['a'] },
    { id: 'c', label: 'Branch C', prerequisites: ['a'] },
    { id: 'a', label: 'Basics' },
    { id: 'e', label: 'Independent' },
  ])
  expect(graph.edges.map(({ from, to }) => `${from}:${to}`).sort()).toEqual(['a:b', 'a:c', 'b:d', 'c:d'])
  const positions = Object.fromEntries(graph.nodes.map((node) => [node.skill.id, node]))
  expect(positions.b.x).toBe(positions.c.x)
  expect(positions.b.y).not.toBe(positions.c.y)
  expect(positions.a.x).toBeLessThan(positions.b.x)
  expect(positions.b.x).toBeLessThan(positions.d.x)
  expect(positions.e.x).toBe(positions.a.x)
})

test('legacy cycles and invalid references cannot break rendering', () => {
  const graph = layoutSkillGraph([
    { id: 'a', label: 'A', prerequisites: ['b', 'b', 'a', 'unknown'] },
    { id: 'b', label: 'B', prerequisites: ['a'] },
  ])
  expect(graph.nodes).toHaveLength(2)
  expect(graph.edges).toHaveLength(1)
  expect(graph.edges[0].start.x).toBeLessThan(graph.edges[0].end.x)
  expect(layoutSkillGraph([]).edges).toEqual([])
})
