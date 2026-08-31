import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  AIRequestError,
  generateProjectConceptMap,
  getProjectConceptMap,
  type ConceptMapDocument,
  type ConceptMapNode,
  type ConceptMapTopic,
} from '../../api'
import { Icon } from './Icon'

interface ConceptMapPanelProps {
  projectId: string
  projectName: string
  onClose: () => void
  /** Opens a linked session in the workspace; the panel closes itself first. */
  onOpenSession?: (sessionId: string) => void
}

const ROW_HEIGHT = 40
const TOPIC_GAP = 18
const TOPIC_X = 16
const CANVAS_PADDING_Y = 16
const COLUMN_GAP = 72
const LINK_ARC_SPACE = 56
const LABEL_DISPLAY_LIMIT = 22

interface LayoutNode {
  id: string
  label: string
  isTopic: boolean
  isNew: boolean
  hasDetail: boolean
  collapsed?: boolean
  childCount?: number
  x: number
  y: number
  width: number
}

interface LayoutEdge {
  from: string
  to: string
}

interface ConceptMapLayout {
  nodes: LayoutNode[]
  edges: LayoutEdge[]
  width: number
  height: number
  childColumnX: number
}

function nodeWidth(label: string): number {
  const display = displayLabel(label)
  let units = 0
  for (const char of display) {
    units += char.charCodeAt(0) > 0x2e7f ? 2 : 1.1
  }
  return Math.max(72, Math.min(300, 30 + units * 7.5))
}

function displayLabel(label: string): string {
  const runes = Array.from(label)
  if (runes.length <= LABEL_DISPLAY_LIMIT) return label
  return runes.slice(0, LABEL_DISPLAY_LIMIT - 1).join('') + '…'
}

function layoutConceptMap(
  doc: ConceptMapDocument,
  collapsedTopics: ReadonlySet<string>,
): ConceptMapLayout {
  const nodes: LayoutNode[] = []
  const edges: LayoutEdge[] = []
  let topicColumnWidth = 0
  for (const topic of doc.topics) {
    topicColumnWidth = Math.max(topicColumnWidth, nodeWidth(topic.label))
  }
  const childColumnX = TOPIC_X + topicColumnWidth + COLUMN_GAP
  let cursorY = CANVAS_PADDING_Y
  let childColumnWidth = 0
  for (const topic of doc.topics) {
    const collapsed = collapsedTopics.has(topic.id)
    const visibleChildren = collapsed ? [] : topic.children
    const blockRows = Math.max(1, visibleChildren.length)
    const blockHeight = blockRows * ROW_HEIGHT
    const topicY = cursorY + blockHeight / 2
    nodes.push({
      id: topic.id,
      label: topic.label,
      isTopic: true,
      isNew: Boolean(topic.new),
      hasDetail: false,
      collapsed,
      childCount: topic.children.length,
      x: TOPIC_X,
      y: topicY,
      width: topicColumnWidth,
    })
    visibleChildren.forEach((child, index) => {
      const width = nodeWidth(child.label)
      childColumnWidth = Math.max(childColumnWidth, width)
      nodes.push({
        id: child.id,
        label: child.label,
        isTopic: false,
        isNew: Boolean(child.new),
        hasDetail: true,
        x: childColumnX,
        y: cursorY + index * ROW_HEIGHT + ROW_HEIGHT / 2,
        width,
      })
      edges.push({ from: topic.id, to: child.id })
    })
    cursorY += blockHeight + TOPIC_GAP
  }
  return {
    nodes,
    edges,
    width: childColumnX + childColumnWidth + LINK_ARC_SPACE + 16,
    height: cursorY - TOPIC_GAP + CANVAS_PADDING_Y,
    childColumnX,
  }
}

function conceptMapErrorMessage(error: unknown): string {
  if (error instanceof AIRequestError) {
    if (error.status === 402) return '余额不足，请先充值后再生成知识地图。'
    if (error.status === 409) return '同一请求正在生成中，请稍候刷新。'
    if (error.status === 422) {
      return '项目还没有可用的转录内容：请先把含转录的云端会话关联到这个项目。'
    }
    if (error.status === 413) return '存储配额已满，请清理项目资料后重试。'
  }
  if (error instanceof Error && error.message) return error.message
  return '知识地图生成失败，请稍后重试。'
}

function findNode(
  doc: ConceptMapDocument,
  nodeId: string,
): { node: ConceptMapNode; topic: ConceptMapTopic } | null {
  for (const topic of doc.topics) {
    for (const child of topic.children) {
      if (child.id === nodeId) return { node: child, topic }
    }
  }
  return null
}

/**
 * Experimental full-screen project concept map (知识地图): a collapsible
 * topic → concept tree generated from every linked session's transcript, with
 * cross-topic links, "new since last version" markers, and per-concept
 * evidence quotes that jump back to the source session.
 */
export function ConceptMapPanel({
  projectId,
  projectName,
  onClose,
  onOpenSession,
}: ConceptMapPanelProps) {
  const [doc, setDoc] = useState<ConceptMapDocument | null>(null)
  const [loading, setLoading] = useState(true)
  const [generating, setGenerating] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [collapsedTopics, setCollapsedTopics] = useState<ReadonlySet<string>>(new Set())
  const [selectedId, setSelectedId] = useState<string | null>(null)

  useEffect(() => {
    let active = true
    setLoading(true)
    setDoc(null)
    setSelectedId(null)
    getProjectConceptMap(projectId)
      .then((response) => {
        if (!active) return
        setDoc(response.map)
      })
      .catch((requestError: unknown) => {
        if (!active) return
        setError(conceptMapErrorMessage(requestError))
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
    }
  }, [projectId])

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKeyDown)
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.removeEventListener('keydown', onKeyDown)
      document.body.style.overflow = previousOverflow
    }
  }, [onClose])

  const generate = useCallback(() => {
    if (generating) return
    setGenerating(true)
    setError(null)
    generateProjectConceptMap(projectId, crypto.randomUUID())
      .then((response) => {
        setDoc(response.map)
        setSelectedId(null)
        setCollapsedTopics(new Set())
      })
      .catch((requestError: unknown) => {
        setError(conceptMapErrorMessage(requestError))
      })
      .finally(() => {
        setGenerating(false)
      })
  }, [generating, projectId])

  const layout = useMemo(
    () => (doc ? layoutConceptMap(doc, collapsedTopics) : null),
    [doc, collapsedTopics],
  )
  const selected = useMemo(
    () => (doc && selectedId ? findNode(doc, selectedId) : null),
    [doc, selectedId],
  )
  const selectedLinks = useMemo(() => {
    if (!doc || !selectedId) return []
    return doc.links
      .filter((link) => link.from === selectedId || link.to === selectedId)
      .map((link) => {
        const otherId = link.from === selectedId ? link.to : link.from
        const other = findNode(doc, otherId)
        return other ? { label: link.label, node: other.node, topic: other.topic } : null
      })
      .filter((entry) => entry !== null)
  }, [doc, selectedId])

  const toggleTopic = (topicId: string) => {
    setCollapsedTopics((previous) => {
      const next = new Set(previous)
      if (next.has(topicId)) {
        next.delete(topicId)
      } else {
        next.add(topicId)
      }
      return next
    })
  }

  const nodeById = new Map<string, LayoutNode>()
  layout?.nodes.forEach((node) => nodeById.set(node.id, node))

  const newConceptCount = doc
    ? doc.topics.reduce(
        (count, topic) => count + topic.children.filter((child) => child.new).length,
        0,
      )
    : 0

  return (
    <div className="dt-cmap" role="dialog" aria-modal="true" aria-label="知识地图">
      <header className="dt-cmap__header">
        <div className="dt-cmap__title">
          <h2>
            知识地图 · {projectName || '未命名项目'}
            <em className="dt-experimental-badge">实验性</em>
          </h2>
          {doc && (
            <p>
              覆盖 {doc.session_count} 场会话 · 更新于{' '}
              {new Date(doc.generated_at).toLocaleString()}
              {newConceptCount > 0 && ` · 本次新增 ${newConceptCount} 个概念`}
              {doc.truncated && ' · 转录过长，部分内容被截断'}
            </p>
          )}
        </div>
        <div className="dt-cmap__actions">
          <button
            className="dt-button dt-button--secondary dt-button--small"
            disabled={generating || loading}
            onClick={generate}
            type="button"
          >
            {generating ? '生成中…' : doc ? '更新地图' : '生成地图'}
          </button>
          <button
            aria-label="关闭知识地图"
            className="dt-icon-button"
            onClick={onClose}
            type="button"
          >
            <Icon name="close" size={18} />
          </button>
        </div>
      </header>

      {error && (
        <p className="dt-cmap__error" role="alert">
          {error}
          <button aria-label="关闭错误提示" onClick={() => setError(null)} type="button">
            <Icon name="close" size={12} />
          </button>
        </p>
      )}

      <div className="dt-cmap__body">
        <div className="dt-cmap__canvas">
          {loading && <p className="dt-cmap__empty">正在加载知识地图…</p>}
          {!loading && !doc && (
            <div className="dt-cmap__empty">
              <Icon name="map" size={36} />
              <h3>为这个项目生成知识地图</h3>
              <p>
                AI 会通读项目里所有已关联会话的转录，整理出「主题 → 概念」的复习地图；
                每个概念都带课堂原文出处。之后每次更新会在旧地图基础上延续，并标出新增内容。
              </p>
              <p className="dt-cmap__hint">生成会调用 AI 并按用量计费。</p>
              <button
                className="dt-button dt-button--primary"
                disabled={generating}
                onClick={generate}
                type="button"
              >
                {generating ? '生成中…' : '生成知识地图'}
              </button>
            </div>
          )}
          {!loading && doc && layout && (
            <svg
              className="dt-cmap__svg"
              height={layout.height}
              role="img"
              width={layout.width}
            >
              {layout.edges.map((edge) => {
                const from = nodeById.get(edge.from)
                const to = nodeById.get(edge.to)
                if (!from || !to) return null
                const startX = from.x + from.width
                const endX = to.x
                const midX = (startX + endX) / 2
                return (
                  <path
                    className="dt-cmap__edge"
                    d={`M ${startX} ${from.y} C ${midX} ${from.y}, ${midX} ${to.y}, ${endX} ${to.y}`}
                    key={`${edge.from}-${edge.to}`}
                  />
                )
              })}
              {doc.links.map((link) => {
                const from = nodeById.get(link.from)
                const to = nodeById.get(link.to)
                if (!from || !to) return null
                const startX = from.x + from.width
                const endX = to.x + to.width
                const arcX =
                  Math.max(startX, endX) + Math.min(LINK_ARC_SPACE, 24 + Math.abs(from.y - to.y) / 8)
                return (
                  <path
                    className="dt-cmap__cross-link"
                    d={`M ${startX} ${from.y} C ${arcX} ${from.y}, ${arcX} ${to.y}, ${endX} ${to.y}`}
                    key={`link-${link.from}-${link.to}`}
                  >
                    <title>
                      {link.label
                        ? `${from.label} —${link.label}→ ${to.label}`
                        : `${from.label} ↔ ${to.label}`}
                    </title>
                  </path>
                )
              })}
              {layout.nodes.map((node) => (
                <g
                  className={[
                    'dt-cmap__node',
                    node.isTopic ? 'dt-cmap__node--topic' : 'dt-cmap__node--concept',
                    node.id === selectedId ? 'is-selected' : '',
                    node.isNew ? 'is-new' : '',
                  ]
                    .filter(Boolean)
                    .join(' ')}
                  key={node.id}
                  onClick={() => {
                    if (node.isTopic) {
                      toggleTopic(node.id)
                    } else {
                      setSelectedId(node.id === selectedId ? null : node.id)
                    }
                  }}
                  transform={`translate(${node.x}, ${node.y - 15})`}
                >
                  <rect height={30} rx={9} width={node.width} />
                  <text x={node.isTopic ? 12 : 14} y={19}>
                    {displayLabel(node.label)}
                  </text>
                  {node.isTopic && node.collapsed && (
                    <text className="dt-cmap__count" x={node.width - 8} y={19}>
                      +{node.childCount}
                    </text>
                  )}
                  {node.isNew && <circle className="dt-cmap__new-dot" cx={5} cy={5} r={4} />}
                  <title>
                    {node.isTopic
                      ? `${node.label}（点击${node.collapsed ? '展开' : '收起'}）`
                      : node.label}
                  </title>
                </g>
              ))}
            </svg>
          )}
        </div>

        {selected && (
          <aside className="dt-cmap__detail">
            <div className="dt-cmap__detail-heading">
              <h3>
                {selected.node.label}
                {selected.node.new && <em className="dt-cmap__new-badge">新</em>}
              </h3>
              <button
                aria-label="关闭概念卡片"
                className="dt-icon-button"
                onClick={() => setSelectedId(null)}
                type="button"
              >
                <Icon name="close" size={14} />
              </button>
            </div>
            <p className="dt-cmap__detail-topic">所属主题：{selected.topic.label}</p>
            {selected.node.summary && <p className="dt-cmap__summary">{selected.node.summary}</p>}
            {(selected.node.evidence?.length ?? 0) > 0 && (
              <div className="dt-cmap__evidence">
                <h4>课堂原文</h4>
                {selected.node.evidence?.map((evidence, index) => (
                  <blockquote key={index}>
                    <p>“{evidence.quote}”</p>
                    <footer>
                      <span>{evidence.session_title || '未命名会话'}</span>
                      {onOpenSession && evidence.session_id && (
                        <button
                          onClick={() => onOpenSession(evidence.session_id)}
                          type="button"
                        >
                          打开会话
                        </button>
                      )}
                    </footer>
                  </blockquote>
                ))}
              </div>
            )}
            {selectedLinks.length > 0 && (
              <div className="dt-cmap__related">
                <h4>相关概念</h4>
                {selectedLinks.map((entry, index) => (
                  <button
                    key={index}
                    onClick={() => setSelectedId(entry.node.id)}
                    type="button"
                  >
                    {entry.node.label}
                    {entry.label ? `（${entry.label}）` : ''}
                  </button>
                ))}
              </div>
            )}
          </aside>
        )}
      </div>

      {doc && (
        <footer className="dt-cmap__legend">
          <span>
            <i className="dt-cmap__legend-new" /> 本次新增
          </span>
          <span>
            <i className="dt-cmap__legend-link" /> 跨主题关联
          </span>
          <span>点击主题可折叠 · 点击概念查看解释与原文</span>
        </footer>
      )}
    </div>
  )
}
