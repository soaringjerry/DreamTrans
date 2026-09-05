import type { DiagnosticsReport, DreamTransProject } from '../shared/types'

export function projectOptions(select: HTMLSelectElement, label: string, projects: DreamTransProject[] = []): void {
  select.replaceChildren(new Option(label, ''))
  for (const project of projects) select.add(new Option(project.name, project.id))
}

export function reportContents(container: HTMLElement, report: DiagnosticsReport): void {
  const table = document.createElement('table')
  for (const check of report.checks) {
    const row = table.insertRow()
    row.insertCell().textContent = check.label
    const mark = document.createElement('span')
    mark.className = check.ok === null ? 'na' : check.ok ? 'ok' : 'bad'
    mark.textContent = check.ok === null ? '—' : check.ok ? 'OK' : 'NO'
    row.insertCell().append(mark)
    row.insertCell().textContent = check.detail
  }
  const title = document.createElement('h4')
  title.textContent = 'MODTYPE 分布'
  const list = document.createElement('ul')
  const entries = Object.entries(report.modtypes).sort((a, b) => b[1] - a[1])
  for (const [type, count] of entries) {
    const item = document.createElement('li')
    item.textContent = `${type}: ${count}`
    list.append(item)
  }
  if (!entries.length) {
    const item = document.createElement('li')
    item.textContent = '无'
    list.append(item)
  }
  container.replaceChildren(table, title, list)
}
