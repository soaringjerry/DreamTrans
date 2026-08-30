import { useCallback, useEffect, useRef, useState } from 'react'
import {
  costEditorScale,
  deleteCostOverride,
  formatUSD,
  getBillingCatalog,
  putCostOverride,
  updateBillingMarkup,
  validateCostOverrideInput,
  validateMarkupInput,
  type BillingCatalog,
  type CostRate,
  type MarkupInput,
  type MarkupOverride,
} from '../../admin/api'
import {
  costEditorUnit,
  formatDate,
  formatNumber,
  formatPercent,
  formatUnitPrice,
  toDateTimeLocal,
  type Runner,
} from './shared'
import { ErrorBanner, Modal } from './ui'

interface CostOverrideDraft {
  rate: CostRate
  cost: string
  sourceLabel: string
  effectiveAt: string
}

function hasCostOverride(rate: CostRate) {
  return Boolean(rate.cost_override_id) || rate.cost_source === 'contract_override'
}

function editableCostSourceLabel(rate: CostRate) {
  return hasCostOverride(rate) ? rate.cost_source_label || '管理员合同价' : '管理员合同价'
}

function costSourceLabel(rate: CostRate) {
  if (hasCostOverride(rate)) return rate.cost_source_label || '管理员合同价'
  if (rate.cost_source === 'manual') return '模型人工成本'
  return '公开目录价'
}

function markupSourceLabel(source: string) {
  if (!source || source === 'default') return '全局默认'
  const [scope, key] = source.split(':', 2)
  if (scope === 'provider') return `Provider ${key || ''}`.trim()
  if (scope === 'category') return `类别 ${key || ''}`.trim()
  if (scope === 'sku') return `SKU ${key || ''}`.trim()
  return source
}

function markupInputOf(catalog: BillingCatalog): MarkupInput {
  return {
    default_markup_percent: catalog.config.default_markup_percent,
    overrides: (catalog.config.overrides || []).map((item) => ({ ...item })),
  }
}

export function CostsPage({ run }: { run: Runner }) {
  const [catalog, setCatalog] = useState<BillingCatalog | null>(null)
  const [markup, setMarkup] = useState(50)
  const [overrides, setOverrides] = useState<MarkupOverride[]>([])
  const [loading, setLoading] = useState(true)
  const [markupError, setMarkupError] = useState('')
  const [markupSaving, setMarkupSaving] = useState(false)
  const [costDraft, setCostDraft] = useState<CostOverrideDraft | null>(null)
  const [costDraftError, setCostDraftError] = useState('')
  const [costSaving, setCostSaving] = useState(false)
  const markupWriteInFlight = useRef(false)
  const costWriteInFlight = useRef(false)

  const adopt = useCallback((next: BillingCatalog) => {
    const input = markupInputOf(next)
    setCatalog(next)
    setMarkup(input.default_markup_percent)
    setOverrides(input.overrides)
    setMarkupError('')
  }, [])

  const load = useCallback(async () => {
    setLoading(true)
    const result = await run(() => getBillingCatalog())
    if (result) adopt(result)
    setLoading(false)
  }, [adopt, run])

  useEffect(() => { void load() }, [load])

  const input: MarkupInput = { default_markup_percent: markup, overrides }
  const inputError = validateMarkupInput(input)
  const dirty = catalog ? JSON.stringify(input) !== JSON.stringify(markupInputOf(catalog)) : false
  const activeRates = (catalog?.rates || []).filter((rate) => rate.is_active)
  const planExamples = catalog?.plan_examples || []
  const standardExample = planExamples.find((item) => item.plan_code === 'free') || planExamples[0]

  async function saveMarkup() {
    if (markupWriteInFlight.current) return
    if (inputError) {
      setMarkupError(inputError)
      return
    }
    markupWriteInFlight.current = true
    setMarkupSaving(true)
    setMarkupError('')
    try {
      const result = await run(() => updateBillingMarkup(input), '加价配置已保存并立即生效', setMarkupError)
      if (result) adopt(result)
    } finally {
      markupWriteInFlight.current = false
      setMarkupSaving(false)
    }
  }

  function updateOverride(index: number, patch: Partial<MarkupOverride>) {
    setOverrides((current) => current.map((item, itemIndex) => itemIndex === index ? { ...item, ...patch } : item))
    setMarkupError('')
  }

  async function saveCostOverride() {
    if (!costDraft || costWriteInFlight.current) return
    setCostDraftError('')
    const editorCost = Number(costDraft.cost)
    if (!Number.isFinite(editorCost) || editorCost < 0) {
      setCostDraftError('合同成本必须是有效的非负数字')
      return
    }
    const cost = editorCost / costEditorScale(costDraft.rate.unit_type)
    const effectiveAt = costDraft.effectiveAt ? new Date(costDraft.effectiveAt) : null
    if (!costDraft.sourceLabel.trim() || (effectiveAt && Number.isNaN(effectiveAt.getTime()))) {
      setCostDraftError('请填写成本来源，并检查生效时间')
      return
    }
    const overrideInput = {
      provider: costDraft.rate.provider,
      sku: costDraft.rate.sku,
      service: costDraft.rate.service,
      unit_type: costDraft.rate.unit_type,
      cost_per_unit_usd: cost,
      source_label: costDraft.sourceLabel.trim(),
      ...(effectiveAt ? { effective_at: effectiveAt.toISOString() } : {}),
    }
    const validationError = validateCostOverrideInput(overrideInput)
    if (validationError) {
      setCostDraftError(validationError)
      return
    }
    costWriteInFlight.current = true
    setCostSaving(true)
    try {
      const result = await run(() => putCostOverride(overrideInput), '合同成本已保存', setCostDraftError)
      if (result) {
        adopt(result)
        setCostDraft(null)
      }
    } finally {
      costWriteInFlight.current = false
      setCostSaving(false)
    }
  }

  async function removeCostOverride() {
    if (!costDraft || costWriteInFlight.current) return
    setCostDraftError('')
    costWriteInFlight.current = true
    setCostSaving(true)
    try {
      const result = await run(() => deleteCostOverride({
        provider: costDraft.rate.provider,
        sku: costDraft.rate.sku,
        service: costDraft.rate.service,
        unit_type: costDraft.rate.unit_type,
      }), '合同成本已撤销，恢复公开目录价', setCostDraftError)
      if (result) {
        adopt(result)
        setCostDraft(null)
      }
    } finally {
      costWriteInFlight.current = false
      setCostSaving(false)
    }
  }

  const costDraftUnchanged = costDraft
    ? Number(costDraft.cost) === costDraft.rate.effective_cost_per_unit_usd * costEditorScale(costDraft.rate.unit_type)
      && costDraft.sourceLabel === editableCostSourceLabel(costDraft.rate)
      && costDraft.effectiveAt === (hasCostOverride(costDraft.rate) ? toDateTimeLocal(costDraft.rate.effective_at) : '')
    : true

  return (
    <>
      <div className="pa-stack">
        <section className="pa-card pa-section">
          <div className="pa-section__heading">
            <div>
              <h2>成本加价</h2>
              <p>售价 = 有效上游成本 ×（1 + 加价率）×（1 − 会员折扣）。保存后立即作用于之后的请求。</p>
            </div>
            <div className="pa-heading-badges">
              <span className="pa-pill">目录 {catalog?.config.catalog_version || '—'}</span>
              {catalog?.config.updated_at && <span className="pa-pill">更新于 {formatDate(catalog.config.updated_at)}</span>}
            </div>
          </div>
          <div className="pa-form-grid">
            <label><span>默认成本加价率</span><div className="pa-input-suffix"><input disabled={loading || markupSaving} min="0" onChange={(event) => {
              setMarkup(Number(event.target.value))
              setMarkupError('')
            }} step="1" type="number" value={markup} /><i>%</i></div></label>
            <div className="pa-form-result"><small>对应标准价毛利率</small><strong>{formatPercent(markup / (100 + markup) * 100)}</strong></div>
            <div className="pa-form-result"><small>实时转写标准价</small><strong>{standardExample ? `${formatUSD(standardExample.realtime_hour_usd)} / 小时` : '—'}</strong></div>
          </div>

          <div className="pa-subsection">
            <div className="pa-subsection__heading">
              <div><h3>分级加价</h3><p>具体 SKU 优先于服务类别，类别优先于 Provider。</p></div>
              <button className="pa-button pa-button--quiet" disabled={loading || markupSaving} onClick={() => {
                setOverrides((current) => [...current, { scope_type: 'provider', scope_key: '', markup_percent: markup }])
                setMarkupError('')
              }} type="button">添加覆盖</button>
            </div>
            {overrides.length === 0 ? <div className="pa-empty">当前全部使用默认加价率。</div> : (
              <div className="pa-override-list">{overrides.map((override, index) => (
                <div className="pa-override" key={index}>
                  <select disabled={markupSaving} onChange={(event) => updateOverride(index, { scope_type: event.target.value as MarkupOverride['scope_type'] })} value={override.scope_type}>
                    <option value="provider">Provider</option><option value="category">服务类别</option><option value="sku">具体 SKU</option>
                  </select>
                  <input disabled={markupSaving} onChange={(event) => updateOverride(index, { scope_key: event.target.value })} placeholder={override.scope_type === 'provider' ? '例如 speechmatics' : override.scope_type === 'category' ? '例如 llm' : '例如 gpt-4.1-mini'} value={override.scope_key} />
                  <input disabled={markupSaving} min="0" onChange={(event) => updateOverride(index, { markup_percent: Number(event.target.value) })} type="number" value={override.markup_percent} />
                  <span>%</span>
                  <button disabled={markupSaving} onClick={() => {
                    setOverrides((current) => current.filter((_, itemIndex) => itemIndex !== index))
                    setMarkupError('')
                  }} type="button">删除</button>
                </div>
              ))}</div>
            )}
          </div>

          {(markupError || (dirty && inputError)) && (
            <div className="pa-callout pa-callout--danger" role="alert">{markupError || inputError}</div>
          )}
          <div className="pa-button-row pa-button-row--split">
            <button className="pa-button pa-button--quiet" disabled={!dirty || markupSaving || !catalog} onClick={() => { if (catalog) adopt(catalog) }} type="button">撤销修改</button>
            <button className="pa-button pa-button--primary" disabled={!dirty || loading || markupSaving || Boolean(inputError)} onClick={() => void saveMarkup()} type="button">
              {markupSaving ? '正在保存…' : '保存并生效'}
            </button>
          </div>
        </section>

        <section className="pa-card pa-section">
          <div className="pa-section__heading">
            <div><h2>每小时示例</h2><p>以实时转写（Speechmatics Enhanced）为例，展示各套餐每小时售价与毛利。</p></div>
          </div>
          {loading ? (
            <div className="pa-summary-grid"><span className="pa-skeleton pa-skeleton--panel" /><span className="pa-skeleton pa-skeleton--panel" /></div>
          ) : planExamples.length === 0 ? (
            <div className="pa-empty">还没有可用的套餐示例。</div>
          ) : (
            <div className="pa-summary-grid">
              {planExamples.map((example) => (
                <div key={example.plan_code}>
                  <small>{example.plan_name}{example.discount_percent > 0 ? ` · 折扣 ${formatPercent(example.discount_percent)}` : ' · 标准价'}</small>
                  <strong>{formatUSD(example.realtime_hour_usd)} / 小时</strong>
                  <em>上游 {formatUSD(example.realtime_upstream_usd)} · 毛利 {formatPercent(example.realtime_gross_margin_percent)}</em>
                </div>
              ))}
            </div>
          )}
        </section>

        <section className="pa-card pa-section">
          <div className="pa-section__heading">
            <div><h2>成本与标准售价</h2><p>公开价来自内置目录 {catalog?.builtin_catalog_version || '—'}；有效价可被合同价或人工模型成本覆盖。标准售价未计入会员折扣。</p></div>
          </div>
          <div className="pa-table-wrap"><table className="pa-table--wide">
            <thead><tr><th>SKU</th><th>公开成本</th><th>有效成本</th><th>加价</th><th>标准售价</th><th>操作</th></tr></thead>
            <tbody>
              {loading && <tr><td className="pa-table-empty" colSpan={6}>正在加载成本目录…</td></tr>}
              {!loading && activeRates.length === 0 && <tr><td className="pa-table-empty" colSpan={6}>成本目录为空。</td></tr>}
              {!loading && activeRates.map((rate) => (
                <tr key={`${rate.provider}-${rate.service}-${rate.sku}-${rate.unit_type}`}>
                  <td><strong>{rate.sku}</strong><small>{rate.provider} · {rate.service} · {rate.unit_type}</small></td>
                  <td>{formatUnitPrice(rate.public_cost_per_unit_usd, rate.unit_type)}</td>
                  <td>
                    <span>{formatUnitPrice(rate.effective_cost_per_unit_usd, rate.unit_type)}</span>
                    <small>{costSourceLabel(rate)} · 生效 {formatDate(rate.effective_at)}</small>
                  </td>
                  <td>{formatNumber(rate.markup_percent)}% <small>{markupSourceLabel(rate.markup_source)}</small></td>
                  <td><strong>{formatUnitPrice(rate.retail_per_unit_usd, rate.unit_type)}</strong></td>
                  <td><button className="pa-link-button" disabled={dirty} onClick={() => {
                    setCostDraftError('')
                    setCostDraft({
                      rate,
                      cost: String(rate.effective_cost_per_unit_usd * costEditorScale(rate.unit_type)),
                      sourceLabel: editableCostSourceLabel(rate),
                      effectiveAt: hasCostOverride(rate) ? toDateTimeLocal(rate.effective_at) : '',
                    })
                  }} title={dirty ? '请先保存或撤销未保存的加价修改' : ''} type="button">编辑合同成本</button></td>
                </tr>
              ))}
            </tbody>
          </table></div>
        </section>
      </div>

      {costDraft && (
        <Modal
          footer={(
            <>
              {hasCostOverride(costDraft.rate) && (
                <button className="pa-button pa-button--danger-quiet" disabled={costSaving} onClick={() => void removeCostOverride()} type="button">撤销覆盖</button>
              )}
              <span className="pa-modal__spacer" />
              <button className="pa-button pa-button--quiet" disabled={costSaving} onClick={() => setCostDraft(null)} type="button">取消</button>
              <button className="pa-button pa-button--primary" disabled={costSaving || costDraft.cost === '' || costDraft.sourceLabel.trim() === '' || costDraftUnchanged} onClick={() => void saveCostOverride()} type="button">
                {costSaving ? '正在保存…' : '保存合同成本'}
              </button>
            </>
          )}
          onClose={() => { if (!costSaving) setCostDraft(null) }}
          title="编辑上游合同成本"
        >
          <div className="pa-dialog-form">
            {costDraftError && <ErrorBanner message={costDraftError} />}
            <div className="pa-callout"><strong>{costDraft.rate.sku}</strong><span>{costDraft.rate.provider} · {costDraft.rate.unit_type}</span></div>
            <label><span>公开目录价（{costEditorUnit(costDraft.rate.unit_type)}）</span><input disabled value={costDraft.rate.public_cost_per_unit_usd * costEditorScale(costDraft.rate.unit_type)} /></label>
            <label><span>有效合同价（{costEditorUnit(costDraft.rate.unit_type)}）</span><input autoFocus disabled={costSaving} min="0" onChange={(event) => setCostDraft({ ...costDraft, cost: event.target.value })} required step="0.000001" type="number" value={costDraft.cost} /></label>
            <label><span>成本来源</span><input disabled={costSaving} maxLength={120} onChange={(event) => setCostDraft({ ...costDraft, sourceLabel: event.target.value })} placeholder="例如：Enterprise Contract 2026" required value={costDraft.sourceLabel} /></label>
            <label><span>生效时间（可选）</span><input disabled={costSaving} onChange={(event) => setCostDraft({ ...costDraft, effectiveAt: event.target.value })} type="datetime-local" value={costDraft.effectiveAt} /></label>
            <p className="pa-form-note">留空表示由服务器立即生效；不支持预设未来时间。保存后只影响之后的请求。</p>
          </div>
        </Modal>
      )}
    </>
  )
}
