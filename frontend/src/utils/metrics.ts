export type MetricKind = 'chat' | 'translation' | 'transcript'

export type MetricEvent = {
  kind: MetricKind
  latency_ms?: number
  model?: string
  partial?: boolean
  at: number
}

declare global {
  interface Window { __dt_metrics?: MetricEvent[] }
}

export function emitMetric(ev: Omit<MetricEvent, 'at'> & { at?: number }) {
  const item: MetricEvent = { ...ev, at: ev.at ?? Date.now() }
  if (!Array.isArray(window.__dt_metrics)) window.__dt_metrics = []
  window.__dt_metrics.unshift(item)
  if (window.__dt_metrics.length > 100) window.__dt_metrics.length = 100
  window.dispatchEvent(new CustomEvent('dt-metrics', { detail: item }))
}

export function getMetrics(): MetricEvent[] {
  return Array.isArray(window.__dt_metrics) ? window.__dt_metrics.slice(0, 50) : []
}

