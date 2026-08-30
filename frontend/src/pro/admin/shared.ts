import { formatUSD } from '../../admin/api'

export type Runner = <T>(
  operation: () => Promise<T>,
  success?: string,
  onError?: (message: string) => void,
) => Promise<T | undefined>

export function formatNumber(value: number, digits = 2) {
  return new Intl.NumberFormat('zh-CN', {
    maximumFractionDigits: digits,
    minimumFractionDigits: 0,
  }).format(Number.isFinite(value) ? value : 0)
}

export function formatInteger(value: number) {
  return new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 0 }).format(
    Number.isFinite(value) ? Math.trunc(value) : 0,
  )
}

export function formatPercent(value: number, digits = 1) {
  return `${formatNumber(value, digits)}%`
}

export function formatDate(value?: string | null) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN')
}

export function formatDay(value?: string | null) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleDateString('zh-CN')
}

export function toDateTimeLocal(value?: string) {
  const date = value ? new Date(value) : new Date()
  if (Number.isNaN(date.getTime())) return ''
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000)
  return local.toISOString().slice(0, 16)
}

export function currentMonthKey() {
  const now = new Date()
  return `${now.getUTCFullYear()}-${String(now.getUTCMonth() + 1).padStart(2, '0')}`
}

/** Per-unit cost/price for the catalog table: tokens per million, hours and minutes per unit. */
export function formatUnitPrice(value: number, unitType: string) {
  if (unitType.includes('token')) return `${formatUSD(value * 1_000_000, 4)} / 百万 token`
  if (unitType === 'hour') return `${formatUSD(value, 4)} / 小时`
  if (unitType === 'minute') return `${formatUSD(value, 4)} / 分钟`
  return `${formatUSD(value, 4)} / ${unitType}`
}

export function costEditorUnit(unitType: string) {
  if (unitType.includes('token')) return 'USD / 百万 token'
  if (unitType === 'hour') return 'USD / 小时'
  if (unitType === 'minute') return 'USD / 分钟'
  return `USD / ${unitType}`
}

export function formatBytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let index = 0
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024
    index += 1
  }
  return `${formatNumber(value, index === 0 ? 0 : 1)} ${units[index]}`
}

export function errorMessage(reason: unknown) {
  return reason instanceof Error ? reason.message : '请求失败'
}

export function limitLabel(value: number, unit = '') {
  if (value === -1) return '不限'
  return unit ? `${formatInteger(value)} ${unit}` : formatInteger(value)
}
