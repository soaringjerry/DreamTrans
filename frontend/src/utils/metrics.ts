export type MetricKind = 'chat' | 'translation' | 'transcript'

export type MetricEvent = {
  kind: MetricKind
  latency_ms?: number
  model?: string
  partial?: boolean
  at: number
}

declare global {
  interface Window { __dt_metrics?: MetricEvent[], __dt_metrics_by_kind?: Record<MetricKind, MetricEvent[]> }
}

export function emitMetric(ev: Omit<MetricEvent, 'at'> & { at?: number }) {
  const item: MetricEvent = { ...ev, at: ev.at ?? Date.now() }
  if (!Array.isArray(window.__dt_metrics)) window.__dt_metrics = []
  window.__dt_metrics.unshift(item)
  if (window.__dt_metrics.length > 100) window.__dt_metrics.length = 100
  // per-kind ring buffer to avoid sparsity issues in mixed streams
  if (!window.__dt_metrics_by_kind) window.__dt_metrics_by_kind = { chat: [], translation: [], transcript: [] }
  const arr = window.__dt_metrics_by_kind[item.kind] || []
  arr.unshift(item)
  if (arr.length > 200) arr.length = 200
  window.__dt_metrics_by_kind[item.kind] = arr
  window.dispatchEvent(new CustomEvent('dt-metrics', { detail: item }))
}

export function getMetrics(): MetricEvent[] {
  return Array.isArray(window.__dt_metrics) ? window.__dt_metrics.slice(0, 50) : []
}

export function getMetricsByKind(kind: MetricKind, limit: number = 50): MetricEvent[] {
  const store = window.__dt_metrics_by_kind
  if (!store) return []
  const arr = store[kind] || []
  return arr.slice(0, Math.max(0, limit))
}
