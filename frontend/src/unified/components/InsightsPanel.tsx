import { useCallback, useEffect, useMemo, useState } from 'react'
import { getOptionalAuthHeaders } from '../../api'
import {
  lexSnapshot,
  selectTopLexEntries,
  type LexSnapshot,
} from '../../utils/lexicon'
import {
  isKnown,
  isLearning,
  loadUserLex,
  markKnown,
  markLearning,
} from '../../utils/userLex'
import { subscribeVocabularyRefresh } from '../workspace/vocabularyRefresh'
import { useMessages } from '../../i18n'
import { Icon } from './Icon'

interface InsightsPanelProps {
  assistantEnabled: boolean
  canViewApiMetrics: boolean
  durationLabel: string
  finalSegments: number
  pendingWrites: number
  sessionId: string
  speakers: number
  topWords: Array<{ word: string; count: number }>
  translatedSegments: number
  onExplainTerm: (term: string) => void
}

interface ApiTotals {
  requests: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  per_model?: Record<string, ApiTotals>
}

interface ApiSnapshot {
  chat: ApiTotals
  translate: ApiTotals
  summarize: ApiTotals
  overall: ApiTotals
  last_logs?: Array<{
    ts: string
    feature: string
    model: string
    prompt_tokens: number
    completion_tokens: number
    total_tokens: number
    latency_ms: number
  }>
}

type DisplayFilter = 'all' | 'learning' | 'unknown'
type InsightsTab = 'api' | 'overview' | 'vocabulary'

const backendURL = import.meta.env.VITE_BACKEND_URL || 'http://localhost:8080'
const apiBase = backendURL === '/' ? '' : backendURL
const stopwords = new Set([
  'the', 'a', 'an', 'and', 'or', 'of', 'in', 'on', 'at', 'to', 'for',
  'from', 'by', 'with', 'as', 'is', 'are', 'was', 'were', 'be', 'being',
  'been', 'this', 'that', 'these', 'those', 'it', 'its', 'i', 'you', 'he',
  'she', 'we', 'they', 'me', 'him', 'her', 'us', 'them', 'my', 'your',
  'his', 'our', 'their', 'not', 'no', 'yes', 'do', 'does', 'did', 'have',
  'has', 'had', 'will', 'would', 'can', 'could', 'should', 'may', 'might',
  'if', 'then', 'else', 'than', 'so', 'too', 'very', 'just', 'but',
  'because', 'about', 'into', 'over', 'under', 'again', 'more', 'most',
  'some', 'any', 'each', 'few', 'who', 'what', 'which', 'when', 'where',
  'why', 'how',
])

function normalizedTotals(value?: Partial<ApiTotals>): ApiTotals {
  return {
    requests: value?.requests ?? 0,
    prompt_tokens: value?.prompt_tokens ?? 0,
    completion_tokens: value?.completion_tokens ?? 0,
    total_tokens: value?.total_tokens ?? 0,
    per_model: value?.per_model,
  }
}

function escapeCsv(value: string): string {
  return /[",\n]/.test(value) ? `"${value.replace(/"/g, '""')}"` : value
}

function downloadVocabularyCsv(
  words: Array<[string, number]>,
  terms: Array<[string, number]>,
): void {
  const lines = ['type,key,count']
  const sortedWords = [...words].sort(
    (left, right) => right[1] - left[1] || left[0].localeCompare(right[0]),
  )
  const sortedTerms = [...terms].sort(
    (left, right) => right[1] - left[1] || left[0].localeCompare(right[0]),
  )
  for (const [word, count] of sortedWords) {
    lines.push(`word,${escapeCsv(word)},${count}`)
  }
  for (const [term, count] of sortedTerms) {
    lines.push(`term,${escapeCsv(term)},${count}`)
  }
  const url = URL.createObjectURL(new Blob(
    [`\ufeff${lines.join('\n')}`],
    { type: 'text/csv;charset=utf-8' },
  ))
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = `yufolo-vocabulary-${new Date().toISOString().slice(0, 10)}.csv`
  document.body.appendChild(anchor)
  anchor.click()
  window.setTimeout(() => {
    URL.revokeObjectURL(url)
    anchor.remove()
  }, 0)
}

export function InsightsPanel({
  assistantEnabled,
  canViewApiMetrics,
  durationLabel,
  finalSegments,
  pendingWrites,
  sessionId,
  speakers,
  topWords,
  translatedSegments,
  onExplainTerm,
}: InsightsPanelProps) {
  const i = useMessages().insights
  const [tab, setTab] = useState<InsightsTab>('overview')
  const [apiSnapshot, setApiSnapshot] = useState<ApiSnapshot | null>(null)
  const [apiError, setApiError] = useState('')
  const [apiLoading, setApiLoading] = useState(false)
  const activeTab = !canViewApiMetrics && tab === 'api' ? 'overview' : tab

  const translatedRatio = finalSegments > 0
    ? Math.min(100, Math.round(translatedSegments / finalSegments * 100))
    : 0

  const loadApiMetrics = useCallback(async () => {
    if (!canViewApiMetrics) return
    setApiLoading(true)
    setApiError('')
    try {
      const headers = await getOptionalAuthHeaders()
      const response = await fetch(`${apiBase}/api/metrics`, {
        cache: 'no-store',
        headers,
      })
      if (!response.ok) throw new Error(await response.text())
      setApiSnapshot(await response.json() as ApiSnapshot)
    } catch (reason) {
      setApiError(reason instanceof Error ? reason.message : String(reason))
    } finally {
      setApiLoading(false)
    }
  }, [canViewApiMetrics])

  useEffect(() => {
    if (activeTab !== 'api' || !canViewApiMetrics) return
    void loadApiMetrics()
    const timer = window.setInterval(() => {
      void loadApiMetrics()
    }, 5_000)
    return () => window.clearInterval(timer)
  }, [activeTab, canViewApiMetrics, loadApiMetrics])

  return (
    <div className="dt-insights">
      <div
        aria-label={i.tabsAria}
        className={`dt-segmented dt-segmented--full${
          canViewApiMetrics ? ' dt-segmented--three' : ''
        }`}
        role="tablist"
      >
        <button
          aria-selected={activeTab === 'overview'}
          className={activeTab === 'overview' ? 'is-active' : ''}
          onClick={() => setTab('overview')}
          role="tab"
          type="button"
        >
          {i.tabs.overview}
        </button>
        <button
          aria-selected={activeTab === 'vocabulary'}
          className={activeTab === 'vocabulary' ? 'is-active' : ''}
          onClick={() => setTab('vocabulary')}
          role="tab"
          type="button"
        >
          {i.tabs.vocabulary}
        </button>
        {canViewApiMetrics && (
          <button
            aria-selected={activeTab === 'api'}
            className={activeTab === 'api' ? 'is-active' : ''}
            onClick={() => setTab('api')}
            role="tab"
            type="button"
          >
            {i.tabs.api}
          </button>
        )}
      </div>

      {activeTab === 'overview' && (
        <>
          <div className="dt-stat-grid">
            <Stat icon="wave" label={i.stats.duration} value={durationLabel} />
            <Stat icon="message" label={i.stats.segments} value={String(finalSegments)} />
            <Stat icon="user" label={i.stats.speakers} value={String(speakers)} />
            <Stat icon="cloud" label={i.stats.pending} value={String(pendingWrites)} />
          </div>

          <section className="dt-insights__section">
            <div className="dt-insights__heading">
              <div>
                <p className="dt-eyebrow">Translation</p>
                <h3>{i.translation}</h3>
              </div>
              <strong>{translatedRatio}%</strong>
            </div>
            <div
              aria-label={i.translationAria(translatedRatio)}
              aria-valuemax={100}
              aria-valuemin={0}
              aria-valuenow={translatedRatio}
              className="dt-progress"
              role="progressbar"
            >
              <span style={{ width: `${translatedRatio}%` }} />
            </div>
          </section>

          <section className="dt-insights__section">
            <div className="dt-insights__heading">
              <div>
                <p className="dt-eyebrow">Vocabulary</p>
                <h3>{i.frequentWords}</h3>
              </div>
            </div>
            {topWords.length > 0 ? (
              <div className="dt-word-cloud">
                {topWords.map(({ word, count }) => (
                  <span key={word}>{word}<small>{count}</small></span>
                ))}
              </div>
            ) : (
              <p className="dt-muted">{i.startsAfterRecording}</p>
            )}
          </section>
        </>
      )}

      {activeTab === 'vocabulary' && (
        <VocabularyPanel
          key={sessionId || 'empty'}
          assistantEnabled={assistantEnabled}
          onExplainTerm={onExplainTerm}
          sessionId={sessionId}
        />
      )}

      {activeTab === 'api' && canViewApiMetrics && (
        <ApiMetrics
          error={apiError}
          loading={apiLoading}
          onRefresh={loadApiMetrics}
          snapshot={apiSnapshot}
        />
      )}
    </div>
  )
}

interface VocabularyPanelProps {
  assistantEnabled: boolean
  onExplainTerm: (term: string) => void
  sessionId: string
}

function emptyLexSnapshot(): LexSnapshot {
  return { words: [], bigrams: [], total: 0 }
}

function VocabularyPanel({
  assistantEnabled,
  onExplainTerm,
  sessionId,
}: VocabularyPanelProps) {
  const v = useMessages().insights.vocabulary
  const [snapshot, setSnapshot] = useState<LexSnapshot>(() => (
    sessionId ? lexSnapshot(sessionId) : emptyLexSnapshot()
  ))
  const [displayFilter, setDisplayFilter] = useState<DisplayFilter>('all')
  const [excludeStopwords, setExcludeStopwords] = useState(true)
  const [minimumLength, setMinimumLength] = useState(3)
  const [search, setSearch] = useState('')
  const [topN, setTopN] = useState(30)

  useEffect(() => {
    if (!sessionId) return
    return subscribeVocabularyRefresh({
      eventSource: window,
      onRefresh: () => setSnapshot(lexSnapshot(sessionId)),
      sessionId,
      timer: {
        clear: (handle) => window.clearTimeout(handle as number),
        set: (callback, delayMs) => window.setTimeout(callback, delayMs),
      },
    })
  }, [sessionId])

  const vocabulary = useMemo(() => {
    const userLexicon = loadUserLex()
    const query = search.trim().toLowerCase()
    const wordMatches = (word: string) => (
      word.length >= minimumLength
      && (!excludeStopwords || !stopwords.has(word))
      && (!query || word.includes(query))
      && (displayFilter !== 'unknown' || !userLexicon.known[word])
      && (displayFilter !== 'learning' || Boolean(userLexicon.learning[word]))
    )
    const words = snapshot.words.filter(([word]) => wordMatches(word))
    const terms = snapshot.bigrams
      .filter(([term, count]) => {
        if (count < 2 || (query && !term.includes(query))) return false
        const parts = term.split(' ')
        if (excludeStopwords && parts.some((part) => stopwords.has(part))) return false
        if (parts.every((part) => part.length < minimumLength)) return false
        if (
          displayFilter === 'unknown'
          && parts.some((part) => Boolean(userLexicon.known[part]))
        ) return false
        if (
          displayFilter === 'learning'
          && !parts.some((part) => Boolean(userLexicon.learning[part]))
        ) return false
        return true
      })
    return {
      total: snapshot.total,
      uniqueTerms: snapshot.bigrams.length,
      uniqueWords: snapshot.words.length,
      terms,
      words,
    }
  }, [
    displayFilter,
    excludeStopwords,
    minimumLength,
    search,
    snapshot,
  ])

  const visibleWords = useMemo(
    () => selectTopLexEntries(vocabulary.words, topN),
    [topN, vocabulary.words],
  )
  const visibleTerms = useMemo(
    () => selectTopLexEntries(vocabulary.terms, topN),
    [topN, vocabulary.terms],
  )

  return (
    <div className="dt-vocabulary">
      <div className="dt-vocabulary__stats">
        <span><strong>{vocabulary.total}</strong> {v.tokens}</span>
        <span><strong>{vocabulary.uniqueWords}</strong> {v.words}</span>
        <span><strong>{vocabulary.uniqueTerms}</strong> {v.terms}</span>
      </div>
      <div className="dt-vocabulary__controls">
        <label>
          <span>{v.search}</span>
          <input
            onChange={(event) => setSearch(event.target.value)}
            placeholder={v.searchPlaceholder}
            type="search"
            value={search}
          />
        </label>
        <label>
          <span>{v.display}</span>
          <select
            onChange={(event) => setDisplayFilter(event.target.value as DisplayFilter)}
            value={displayFilter}
          >
            <option value="all">{v.filters.all}</option>
            <option value="unknown">{v.filters.unknown}</option>
            <option value="learning">{v.filters.learning}</option>
          </select>
        </label>
        <label>
          <span>Top</span>
          <select
            onChange={(event) => setTopN(Number(event.target.value))}
            value={topN}
          >
            <option value={20}>20</option>
            <option value={30}>30</option>
            <option value={50}>50</option>
            <option value={100}>100</option>
          </select>
        </label>
        <label>
          <span>{v.shortest}</span>
          <input
            max={10}
            min={1}
            onChange={(event) => setMinimumLength(
              Math.max(1, Math.min(10, Number(event.target.value) || 1)),
            )}
            type="number"
            value={minimumLength}
          />
        </label>
      </div>
      <div className="dt-vocabulary__toolbar">
        <label className="dt-vocabulary__checkbox">
          <input
            checked={excludeStopwords}
            onChange={(event) => setExcludeStopwords(event.target.checked)}
            type="checkbox"
          />
          {v.excludeStopwords}
        </label>
        <button
          className="dt-button dt-button--secondary dt-button--small"
          disabled={vocabulary.words.length === 0 && vocabulary.terms.length === 0}
          onClick={() => downloadVocabularyCsv(vocabulary.words, vocabulary.terms)}
          type="button"
        >
          {v.downloadCsv}
        </button>
      </div>

      <VocabularyList
        assistantEnabled={assistantEnabled}
        items={visibleWords}
        kind="word"
        onExplain={onExplainTerm}
      />
      <VocabularyList
        assistantEnabled={assistantEnabled}
        items={visibleTerms}
        kind="term"
        onExplain={onExplainTerm}
      />
      {!sessionId && <p className="dt-muted">{v.startFirst}</p>}
    </div>
  )
}

interface VocabularyListProps {
  assistantEnabled: boolean
  items: Array<[string, number]>
  kind: 'term' | 'word'
  onExplain: (term: string) => void
}

function VocabularyList({
  assistantEnabled,
  items,
  kind,
  onExplain,
}: VocabularyListProps) {
  const v = useMessages().insights.vocabulary
  return (
    <section className="dt-vocabulary__section">
      <div className="dt-insights__heading">
        <h3>{kind === 'word' ? v.wordFrequency : v.termFrequency}</h3>
        <small>{v.rows(items.length)}</small>
      </div>
      {items.length === 0 ? (
        <p className="dt-muted">{v.empty}</p>
      ) : (
        <div className="dt-vocabulary__list">
          {items.map(([term, count]) => (
            <article key={term}>
              <span>
                <strong className={
                  kind === 'word'
                    ? isLearning(term)
                      ? 'is-learning'
                      : isKnown(term)
                        ? 'is-known'
                        : ''
                    : ''
                }>
                  {term}
                </strong>
                <small>{count}</small>
              </span>
              <div>
                <button
                  disabled={!assistantEnabled}
                  onClick={() => onExplain(term)}
                  title={assistantEnabled ? v.explainAi : v.aiOff}
                  type="button"
                >
                  {v.explain}
                </button>
                {kind === 'word' && (
                  <>
                    <button
                      className={isKnown(term) ? 'is-active' : ''}
                      onClick={() => markKnown(term, !isKnown(term))}
                      type="button"
                    >
                      {isKnown(term) ? v.mastered : v.master}
                    </button>
                    <button
                      className={isLearning(term) ? 'is-active' : ''}
                      onClick={() => markLearning(term, !isLearning(term))}
                      type="button"
                    >
                      {isLearning(term) ? v.learning : v.learn}
                    </button>
                  </>
                )}
              </div>
            </article>
          ))}
        </div>
      )}
    </section>
  )
}

interface ApiMetricsProps {
  error: string
  loading: boolean
  onRefresh: () => Promise<void>
  snapshot: ApiSnapshot | null
}

function ApiMetrics({ error, loading, onRefresh, snapshot }: ApiMetricsProps) {
  const a = useMessages().insights.api
  const overall = normalizedTotals(snapshot?.overall)
  return (
    <div className="dt-api-metrics">
      <div className="dt-summary__toolbar">
        <span className="dt-muted">{a.refreshHint}</span>
        <button
          className="dt-button dt-button--secondary dt-button--small"
          disabled={loading}
          onClick={() => { void onRefresh() }}
          type="button"
        >
          {loading ? a.refreshing : a.refresh}
        </button>
      </div>
      {error && <p className="dt-inline-error">{a.loadFailed(error)}</p>}
      {!snapshot ? (
        <div className="dt-empty dt-empty--compact">
          <span>{loading ? a.loading : a.empty}</span>
        </div>
      ) : (
        <>
          <div className="dt-stat-grid">
            <Stat icon="cloud" label={a.totalRequests} value={overall.requests.toLocaleString()} />
            <Stat icon="message" label={a.totalTokens} value={overall.total_tokens.toLocaleString()} />
            <Stat icon="archive" label="Prompt" value={overall.prompt_tokens.toLocaleString()} />
            <Stat
              icon="sparkles"
              label="Completion"
              value={overall.completion_tokens.toLocaleString()}
            />
          </div>
          <div className="dt-api-metrics__features">
            {([
              ['chat', a.chat, snapshot.chat],
              ['translate', a.translate, snapshot.translate],
              ['summarize', a.summarize, snapshot.summarize],
            ] as const).map(([key, label, raw]) => {
              const totals = normalizedTotals(raw)
              return (
                <article key={key}>
                  <strong>{label}</strong>
                  <span>{a.requests(totals.requests.toLocaleString())}</span>
                  <span>{totals.total_tokens.toLocaleString()} tokens</span>
                </article>
              )
            })}
          </div>
          {snapshot.last_logs && snapshot.last_logs.length > 0 && (
            <section className="dt-vocabulary__section">
              <div className="dt-insights__heading">
                <h3>{a.recent}</h3>
                <small>{a.maxLogs}</small>
              </div>
              <div className="dt-api-metrics__logs">
                {snapshot.last_logs.slice(-20).reverse().map((log, index) => (
                  <article key={`${log.ts}-${index}`}>
                    <span>
                      <strong>{log.feature}</strong>
                      <small>{new Date(log.ts).toLocaleTimeString()}</small>
                    </span>
                    <span>
                      <strong>{log.total_tokens.toLocaleString()} tokens</strong>
                      <small>{log.model} · {Math.round(log.latency_ms)}ms</small>
                    </span>
                  </article>
                ))}
              </div>
            </section>
          )}
        </>
      )}
    </div>
  )
}

interface StatProps {
  icon: 'archive' | 'cloud' | 'message' | 'sparkles' | 'user' | 'wave'
  label: string
  value: string
}

function Stat({ icon, label, value }: StatProps) {
  return (
    <article className="dt-stat">
      <Icon name={icon} size={18} />
      <strong>{value}</strong>
      <span>{label}</span>
    </article>
  )
}
