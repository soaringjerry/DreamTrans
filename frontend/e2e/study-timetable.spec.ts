import { test, expect, type Page } from '@playwright/test'
import { layoutTimetable } from '../src/study/timetable'
import type { CourseSlot } from '../src/api'

const slotOf = (over: Partial<CourseSlot>): CourseSlot => ({
  id: 'slot', project_id: 'course', weekday: 1, start: '10:00', end: '12:00', timezone: 'UTC', label: '', created_at: '', ...over,
})

test('calendar layout widens to the slots and shares a column between overlapping classes', () => {
  const empty = layoutTimetable([])
  expect([empty.startHour, empty.endHour, empty.blocks]).toEqual([8, 18, []])
  const layout = layoutTimetable([
    slotOf({ id: 'early', weekday: 1, start: '07:30', end: '09:00' }),
    slotOf({ id: 'late', weekday: 5, start: '18:00', end: '20:30' }),
    slotOf({ id: 'a', weekday: 3, start: '10:00', end: '12:00' }),
    slotOf({ id: 'b', weekday: 3, start: '11:00', end: '13:00', project_id: 'other' }),
    slotOf({ id: 'c', weekday: 3, start: '14:00', end: '15:00' }),
    slotOf({ id: 'broken', weekday: 3, start: '15:00', end: '14:00' }),
    slotOf({ id: 'sunday-out', weekday: 8 }),
  ])
  expect([layout.startHour, layout.endHour]).toEqual([7, 21])
  expect(layout.hours).toHaveLength(14)
  const byId = Object.fromEntries(layout.blocks.map((block) => [block.slot.id, block]))
  expect(Object.keys(byId).sort()).toEqual(['a', 'b', 'c', 'early', 'late'])
  expect(byId.early.day).toBe(0)
  expect(byId.early.top).toBeCloseTo((30 / (14 * 60)) * 100)
  expect(byId.a.width).toBe(50)
  expect(byId.b.width).toBe(50)
  expect(byId.a.left).toBe(0)
  expect(byId.b.left).toBe(50)
  expect(byId.c.width).toBe(100)
  expect(byId.late.top + byId.late.height).toBeCloseTo(((20.5 - 7) / 14) * 100)
})

const courseA = { id: '11111111-1111-4111-8111-111111111111', name: 'PSY2041', context_mode: 'smart', max_context_tokens: 64000 }
const courseB = { id: '22222222-2222-4222-8222-222222222222', name: 'STA1010', context_mode: 'smart', max_context_tokens: 64000 }
const slotA = { id: '33333333-3333-4333-8333-333333333333', project_id: courseA.id, project_name: courseA.name, weekday: 1, start: '10:00', end: '12:00', timezone: 'Australia/Melbourne', label: 'Lecture', created_at: '2026-03-01T00:00:00Z' }

async function setup(page: Page) {
  const user = { id: 'timetable-user', tenant_id: 'tenant', email: 'timetable@example.test', name: 'Study', role: 'user', is_active: true }
  const token = `e2e.${Buffer.from(JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 3600 })).toString('base64url')}.e2e`
  await page.addInitScript(({ user, token }) => {
    localStorage.setItem('dt_user', JSON.stringify(user))
    localStorage.setItem('dt_access_token', token)
    localStorage.setItem('dt_refresh_token', 'refresh')
  }, { user, token })
  const calls = { added: [] as unknown[], deleted: [] as string[], classify: [] as unknown[], slots: [] as typeof slotA[] }
  await page.route(/^https?:\/\/[^/]+\/api\//, async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname
    const method = route.request().method()
    let body: unknown = {}
    let status = 200
    if (path === '/api/system/access') body = { authentication_enabled: true, rag_enabled: true }
    else if (path === '/api/user/profile') body = { user }
    else if (path === '/api/user/balance') body = { available_usd: 20 }
    else if (path === '/api/ai/projects') body = { projects: [courseA, courseB] }
    else if (path === '/api/ai/timetable') body = { slots: calls.slots }
    else if (path === '/api/ai/timetable/classify') {
      const request = route.request().postDataJSON() as { apply: boolean }
      calls.classify.push(request)
      body = {
        preview: !request.apply,
        scanned: 5,
        kept: 2,
        unmatched: 1,
        applied: request.apply ? 2 : 0,
        assignments: [
          { session_id: 's1', title: 'Monday lecture', started_at: '2026-03-02T23:05:00Z', duration_seconds: 6600, project_id: courseA.id, slot_id: slotA.id, overlap_minutes: 108, change: 'assign' },
          { session_id: 's2', title: 'Moved tutorial', started_at: '2026-03-09T23:00:00Z', duration_seconds: 0, from_project_id: courseB.id, project_id: courseA.id, slot_id: slotA.id, overlap_minutes: 0, change: 'move' },
        ],
      }
    } else if (path.endsWith('/timetable') && method === 'POST') {
      const request = route.request().postDataJSON() as Record<string, unknown>
      calls.added.push(request)
      const slot = { ...slotA, ...request, id: `44444444-4444-4444-8444-44444444444${calls.added.length}`, project_name: undefined }
      calls.slots = [...calls.slots, { ...slot, project_name: courseA.name }]
      status = 201
      body = { slot }
    } else if (path.includes('/timetable/') && method === 'DELETE') {
      const slotId = path.slice(path.lastIndexOf('/') + 1)
      calls.deleted.push(slotId)
      calls.slots = calls.slots.filter(({ id }) => id !== slotId)
      body = { success: true }
    } else if (path.endsWith('/timetable')) body = { slots: calls.slots.filter(({ project_id }) => path.includes(project_id)) }
    else if (path.endsWith('/sources')) body = { sources: [] }
    else if (path.endsWith('/sessions')) body = { sessions: [{ id: 'session', title: 'Filed class', started_at: '2026-03-02T23:05:00Z', duration_seconds: 6600, assigned_by: 'timetable' }] }
    else if (path.endsWith('/skill-map')) body = { artifact: null, map: null }
    else if (path.endsWith('/study/state')) body = { states: [], continue: null }
    else if (path.endsWith('/study/weeks')) body = { weeks: [], behind_weeks: [], current_week: 1, unassigned: { sources: [], sessions: [], skills: [] } }
    else if (path.endsWith('/study/costs')) body = { items: [], summary: { total_usd: 0, by_feature: {} } }
    await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
  })
  await page.goto('/pro/study')
  return calls
}

test('class times are edited per course, drawn on the weekly calendar, and file sessions after a preview', async ({ page }) => {
  const calls = await setup(page)
  const calendar = page.getByRole('region', { name: '每周课表' })
  await expect(calendar.getByText('还没有课程设置上课时间', { exact: false })).toBeVisible()
  await expect(calendar.getByRole('button', { name: '自动归类会话' })).toHaveCount(0)

  await page.getByRole('button', { name: /PSY2041/ }).click()
  await page.getByRole('button', { name: '课程管理', exact: true }).click()
  await expect(page.getByTitle('由课表自动归类；手动移动后不再自动调整')).toHaveText('课表')
  const classTimes = page.getByRole('region', { name: '上课时间' })
  await expect(classTimes.getByText('还没有上课时间。')).toBeVisible()
  await classTimes.getByLabel('星期').selectOption('1')
  await classTimes.getByLabel('开始').fill('10:00')
  await classTimes.getByLabel('结束').fill('12:00')
  await classTimes.getByLabel('备注').fill('Lecture')
  await classTimes.getByRole('button', { name: '添加时段' }).click()
  await expect(classTimes.getByText('周一 10:00–12:00')).toBeVisible()
  expect(calls.added).toHaveLength(1)
  const added = calls.added[0] as Record<string, unknown>
  expect(added).toMatchObject({ weekday: 1, start: '10:00', end: '12:00', label: 'Lecture' })
  expect(typeof added.timezone).toBe('string')
  expect(String(added.timezone).length).toBeGreaterThan(0)

  await classTimes.getByLabel('开始').fill('14:00')
  await classTimes.getByLabel('结束').fill('15:00')
  await classTimes.getByRole('button', { name: '添加时段' }).click()
  await expect(classTimes.getByText('周一 14:00–15:00')).toBeVisible()
  await classTimes.getByRole('button', { name: '删除时段 周一 14:00–15:00' }).click()
  await expect(classTimes.getByText('周一 14:00–15:00')).toHaveCount(0)
  expect(calls.deleted).toEqual(['44444444-4444-4444-8444-444444444442'])

  await page.getByRole('button', { name: '全部课程' }).click()
  const block = calendar.getByTitle(/PSY2041 · 周一 10:00–12:00 · Lecture/)
  await expect(block).toBeVisible()
  await expect(calendar.getByRole('columnheader', { name: '周一' })).toBeVisible()

  await calendar.getByRole('button', { name: '自动归类会话' }).click()
  const preview = page.getByRole('region', { name: '自动归类会话' })
  await expect(preview.getByText('扫描了 5 场会话：2 场已在对应课程，1 场没有匹配的上课时间。')).toBeVisible()
  await expect(preview.getByText('Monday lecture')).toBeVisible()
  await expect(preview.getByText('重叠 108 分钟')).toBeVisible()
  await expect(preview.getByText('按开始时间 · 原在 STA1010')).toBeVisible()
  expect(calls.classify).toEqual([{ apply: false }])
  await preview.getByRole('button', { name: '应用 2 项' }).click()
  await expect(calendar.getByRole('status')).toContainText('已把 2 场会话归入课程。')
  expect(calls.classify).toEqual([{ apply: false }, { apply: true }])
  await expect(preview).toHaveCount(0)
})
