import { useEffect, useRef, useState } from 'react'
import { formatUSD, getPublicPricing, type PublicPlan, type PublicPricing } from '../api'
import { Icon, type IconName } from '../unified/components/Icon'
import { LiveDemo } from './LiveDemo'
import './LandingPage.css'

interface Card {
  icon: IconName
  title: string
  body: string
}

const features: Card[] = [
  {
    icon: 'mic',
    title: '实时转录',
    body: '说话人分离、低延迟上屏。长会议、整节课都能稳定记录，不必事后靠记忆补笔记。',
  },
  {
    icon: 'language',
    title: '双语同传翻译',
    body: '上下文感知的 AI 翻译，按整句润色而不是逐词直译；也可切换低延迟机翻作兜底。',
  },
  {
    icon: 'sparkles',
    title: 'AI 问答与沉淀',
    body: '对着整场录音问结论、生成摘要、笔记与行动项，选中任何一句就能让 AI 解释术语。',
  },
  {
    icon: 'cloud',
    title: '云端工作台',
    body: '登录后会话上云，桌面与手机同一套界面；随时导出原文、译文或双语文本。',
  },
]

const studyCards: Card[] = [
  {
    icon: 'archive',
    title: '课程与资料',
    body: '把课堂转录挂到一门课，再拖进教材、课件（PPTX）、论文（PDF）或截图。按周组织，浏览器扩展可一键同步 Moodle 课件。',
  },
  {
    icon: 'map',
    title: '技能地图',
    body: 'AI 通读转录和资料，提炼这门课要求掌握的能力，排成一条有先后顺序的路线，告诉你今天该练哪一站。',
  },
  {
    icon: 'message',
    title: '先讲解，再做题',
    body: '每一站从讲解卡开始，看完再做单选、多选、判断、填空和开放题。不会就直接看解析，不算错；错题会回流到下一次练习。',
  },
  {
    icon: 'language',
    title: '边听边学的学习视图',
    body: '实时转录里切到「学习」视图，超出你 CEFR 水平的词会自动旁注中文短义，专业术语按学科词表优先标出，不消耗翻译额度。',
  },
]

const studyFlow = ['录下这节课', '挂上课件与教材', '生成技能地图', '看讲解卡', '练习并回顾错题']

const scenarios = [
  {
    label: '听课学习',
    title: '一门课，一条能练的路线',
    body: '课堂转录加课件生成技能地图，课后从讲解卡到练习题一路走下去，错题自动回流。',
  },
  {
    label: '跨国会议',
    title: '边听边懂，会后有据可查',
    body: '实时双语阅读模式，行动项与结论一键生成，减少会后整理时间。',
  },
  {
    label: '访谈调研',
    title: '完整保留原话与译文',
    body: '长会话优化写入与回放，支持继续录制同一时间线，访谈内容可导出归档。',
  },
]

const steps = [
  { n: '01', title: '注册账户', body: '邮箱验证后即可进入工作台，附送试用额度，无需绑卡。' },
  { n: '02', title: '开始录音', body: '实时看到原文与译文；暂停、继续都在同一会话里。' },
  { n: '03', title: '问 AI、练一练或导出', body: '生成摘要与行动项，把课堂送进学习空间，或导出文本与本地音频。' },
]

const pillars: Card[] = [
  {
    icon: 'check',
    title: '准确性优先',
    body: '只用增强级识别引擎，不设降级省钱档。专业词汇、多口音、多说话人的真实会议里，差的那几个词往往就是结论本身。',
  },
  {
    icon: 'shield',
    title: '数据自主',
    body: '录音音频只保存在你的设备上，云端只同步文字。转录与译文随时可以导出为文本，删除会话时云端副本一并删除。',
  },
  {
    icon: 'message',
    title: '计费透明',
    body: '美元钱包按秒、按 token 结算：每次调用先按上限预留，完成后按实际用量结算并退回差额。每一笔都有流水可查。',
  },
]

const faqs = [
  {
    q: '怎么收费？',
    a: '按实际用量从美元钱包扣费：转录按音频时长、AI 按 token。每次调用先按估算上限预留，完成后按真实用量结算并退回差额，账单里每一笔都可以核对。',
  },
  {
    q: '开始使用需要绑卡吗？',
    a: '不需要。注册免费并附送试用额度；用完后可以在线充值，充值余额永不过期。会员按月或按年订阅，随时可取消。',
  },
  {
    q: '注册后马上就能用吗？',
    a: '注册后会收到一封验证邮件，点击链接即可激活账户并获得试用额度。没有收到的话，可以在登录页重新发送。',
  },
  {
    q: '我的录音存在哪里？',
    a: '音频只保存在你自己的设备上，云端只同步转录与翻译文字。',
  },
  {
    q: '学习空间会额外收费吗？',
    a: '生成技能地图、讲解卡和练习题会调用 AI，按 token 从同一个钱包扣费，每次生成前后都会显示实际花费。学习视图的难词旁注是本地词表，不产生费用。',
  },
  {
    q: '支持哪些语言？',
    a: '转录支持 50+ 种语言并自动区分说话人；翻译支持常用语言对，可选上下文感知的 AI 翻译或低延迟机器翻译。',
  },
  {
    q: '录音中途断网怎么办？',
    a: '转录与译文持续写入本地存储，网络恢复后自动续传云端；录音本身一直在本机，不会因为断网丢内容。',
  },
  {
    q: '可以随时导出或删除我的数据吗？',
    a: '可以。转录与译文随时可以导出为原文、译文或双语文本；删除会话后云端副本会一并删除，录音本身只在你的设备上，从未上传。',
  },
]

/** Plan feature flags the catalog may carry, in display order. */
const planFeatureLabels: Array<[string, string]> = [
  ['premium_models', '高级 AI 模型'],
  ['byok', '自带 API Key（BYOK）'],
  ['batch', '批量转写'],
  ['custom_prompt', '自定义翻译提示词'],
  ['auto_topup', '余额自动充值'],
  ['export_ledger', '导出账单流水'],
  ['api_access', 'API 访问'],
]

const baseIncluded = ['实时转录 · 说话人分离', '双语同传翻译', 'AI 问答、摘要与行动项', '学习空间：技能地图与练习']

function openWorkspace(path: string) {
  window.location.assign(path)
}

function prefersReducedMotion(): boolean {
  return window.matchMedia('(prefers-reduced-motion: reduce)').matches
}

function useRevealOnScroll<T extends HTMLElement>() {
  const rootRef = useRef<T | null>(null)

  useEffect(() => {
    const root = rootRef.current
    if (!root) return

    if (prefersReducedMotion()) {
      root.querySelectorAll('.lp-reveal').forEach((node) => {
        node.classList.add('is-visible')
      })
      return
    }

    const observe = () => {
      const nodes = Array.from(root.querySelectorAll<HTMLElement>('.lp-reveal:not(.is-visible)'))
      if (nodes.length === 0) return () => undefined
      const observer = new IntersectionObserver(
        (entries) => {
          for (const entry of entries) {
            if (!entry.isIntersecting) continue
            entry.target.classList.add('is-visible')
            observer.unobserve(entry.target)
          }
        },
        { threshold: 0.14, rootMargin: '0px 0px -6% 0px' },
      )
      for (const node of nodes) observer.observe(node)
      return () => observer.disconnect()
    }
    // Pricing cards mount after the fetch; re-observe when the DOM grows.
    let disconnect = observe()
    const mutation = new MutationObserver(() => {
      disconnect()
      disconnect = observe()
    })
    mutation.observe(root, { childList: true, subtree: true })
    return () => {
      disconnect()
      mutation.disconnect()
    }
  }, [])

  return rootRef
}

function usePublicPricing() {
  const [pricing, setPricing] = useState<PublicPricing | null>(null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    let active = true
    getPublicPricing()
      .then((data) => { if (active) setPricing(data) })
      .catch(() => { if (active) setFailed(true) })
    return () => { active = false }
  }, [])
  return { pricing, failed }
}

function money(amount: number): string {
  return formatUSD(amount, Number.isInteger(amount) ? 0 : 2)
}

function discountLabel(percent: number): string | null {
  if (percent <= 0) return null
  const factor = (100 - percent) / 10
  return `全部用量 ${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 }).format(factor)} 折`
}

function planBullets(plan: PublicPlan, trialUSD: number, cheapest: PublicPlan | null): string[] {
  const bullets: string[] = []
  const free = plan.price_usd_month <= 0
  if (free) {
    bullets.push(trialUSD > 0 ? `注册即送 ${money(trialUSD)} 试用额度` : '注册免费，充值即用')
    bullets.push(...baseIncluded)
    bullets.push('按量计费，充值余额永不过期')
  } else {
    bullets.push(cheapest && cheapest.code !== plan.code ? `包含「${cheapest.name}」全部能力` : '全部核心能力')
    const discount = discountLabel(plan.usage_discount_percent)
    if (discount) bullets.push(discount)
    for (const [key, label] of planFeatureLabels) {
      if (plan.features[key]) bullets.push(label)
    }
  }
  if (plan.max_concurrent_sessions > 1) bullets.push(`${plan.max_concurrent_sessions} 路并发转录`)
  if (plan.retention_days > 0) bullets.push(`云端会话保留 ${plan.retention_days} 天`)
  if (plan.storage_gb > 0 && !free) bullets.push(`${plan.storage_gb} GB 资料存储`)
  return bullets
}

function yearlyNote(plan: PublicPlan): string | null {
  if (plan.price_usd_month <= 0 || plan.price_usd_year <= 0) return null
  const monthsFree = 12 - plan.price_usd_year / plan.price_usd_month
  if (monthsFree >= 0.5) {
    return `按年 ${money(plan.price_usd_year)}，相当于免 ${Math.round(monthsFree)} 个月`
  }
  return `按年 ${money(plan.price_usd_year)}`
}

function PricingSection() {
  const { pricing, failed } = usePublicPricing()
  const plans = pricing
    ? [...pricing.plans].sort((a, b) => a.sort - b.sort || a.price_usd_month - b.price_usd_month)
    : []
  const cheapest = plans[0] ?? null
  const featuredCode = plans.find((plan) => plan.price_usd_month > 0)?.code
  const trialUSD = pricing?.trial_credit_usd ?? 0
  const tiers = (pricing?.topup_tiers ?? []).filter((tier) => tier.bonus_percent > 0)

  return (
    <section className="lp-section lp-section--muted" id="pricing" aria-labelledby="lp-pricing-title">
      <div className="lp-section__head lp-reveal">
        <p className="lp-eyebrow">定价</p>
        <h2 id="lp-pricing-title">从免费试用开始，按你的用量付费</h2>
        <p>没有按月清零的「套餐小时数」：充值余额永不过期，会员只提供折扣与高级能力。</p>
      </div>

      {pricing && plans.length > 0 && (
        <>
          <div className={`lp-pricing-grid${plans.length >= 3 ? ' lp-pricing-grid--three' : ''}`}>
            {plans.map((plan, index) => {
              const featured = plan.code === featuredCode
              const note = yearlyNote(plan)
              return (
                <article
                  className={`lp-price-card lp-reveal${featured ? ' lp-price-card--featured' : ''}`}
                  key={plan.code}
                  style={{ ['--lp-delay' as string]: `${index * 90}ms` }}
                >
                  {featured && <span className="lp-price-card__badge">最受欢迎</span>}
                  <h3>{plan.name}</h3>
                  <p className="lp-price-card__price">
                    <strong>{money(plan.price_usd_month)}</strong>
                    <span>/ 月</span>
                  </p>
                  <p className="lp-price-card__tagline">
                    {note ?? (plan.price_usd_month <= 0 ? '注册即用，按量计费' : '按月订阅，随时取消')}
                  </p>
                  {plan.realtime_hour_usd > 0 && (
                    <p className="lp-price-card__hourly">
                      <span>实时转录 + 翻译</span>
                      <strong>{formatUSD(plan.realtime_hour_usd)} / 小时</strong>
                    </p>
                  )}
                  <ul>
                    {planBullets(plan, trialUSD, cheapest).map((bullet) => (
                      <li key={bullet}>
                        <Icon name="check" size={14} />
                        <span>{bullet}</span>
                      </li>
                    ))}
                  </ul>
                  <button
                    className={`lp-btn lp-btn--lg ${featured ? 'lp-btn--primary' : 'lp-btn--secondary'}`}
                    type="button"
                    onClick={() => openWorkspace('/pro')}
                  >
                    {plan.price_usd_month <= 0 ? '免费开始' : `升级 ${plan.name}`}
                  </button>
                </article>
              )
            })}
          </div>
          {tiers.length > 0 && (
            <div className="lp-topup lp-reveal" aria-label="充值加赠">
              {tiers.map((tier) => (
                <span key={tier.amount_usd}>
                  充值 <strong>{money(tier.amount_usd)}</strong> 加赠 <strong>{tier.bonus_percent}%</strong>
                </span>
              ))}
            </div>
          )}
          <p className="lp-pricing-note lp-reveal">
            价格以美元计，按秒结算转录、按 token 结算 AI；
            {pricing.payments_enabled
              ? `支付由 Stripe 处理${pricing.checkout_currency && pricing.checkout_currency !== 'usd' ? `，以 ${pricing.checkout_currency.toUpperCase()} 结算` : ''}，会员随时可取消。`
              : '会员随时可取消。'}
          </p>
        </>
      )}

      {!pricing && !failed && (
        <p className="lp-pricing-note lp-reveal">正在读取最新价格…</p>
      )}
      {failed && (
        <div className="lp-pricing-grid">
          <article className="lp-price-card lp-reveal">
            <h3>按量使用</h3>
            <p className="lp-price-card__tagline">注册免费并附送试用额度，充值余额永不过期。</p>
            <ul>
              {baseIncluded.map((item) => (
                <li key={item}><Icon name="check" size={14} /><span>{item}</span></li>
              ))}
            </ul>
            <button className="lp-btn lp-btn--lg lp-btn--primary" type="button" onClick={() => openWorkspace('/pro')}>
              免费开始
            </button>
          </article>
          <article className="lp-price-card lp-reveal">
            <h3>会员</h3>
            <p className="lp-price-card__tagline">用量折扣、高级 AI 模型与更长的云端保留期。最新价格请在工作台内查看。</p>
            <button className="lp-btn lp-btn--lg lp-btn--secondary" type="button" onClick={() => openWorkspace('/pro')}>
              登录查看价格
            </button>
          </article>
        </div>
      )}
    </section>
  )
}

export default function LandingPage() {
  const rootRef = useRevealOnScroll<HTMLDivElement>()

  return (
    <div className="lp" ref={rootRef}>
      <div className="lp-ambient" aria-hidden="true">
        <span className="lp-orb lp-orb--a" />
        <span className="lp-orb lp-orb--b" />
        <span className="lp-orb lp-orb--c" />
      </div>

      <header className="lp-nav lp-enter lp-enter--nav">
        <a className="lp-brand" href="/">
          <span className="lp-brand__mark">
            <Icon name="wave" size={20} />
          </span>
          <span>
            <strong>Yufolo</strong>
            <small>实时转录 · 翻译 · 学习空间</small>
          </span>
        </a>
        <nav className="lp-nav__links" aria-label="页面导航">
          <a href="#features">能力</a>
          <a href="#study">学习空间</a>
          <a href="#pricing">定价</a>
          <a href="#faq">常见问题</a>
        </nav>
        <div className="lp-nav__actions">
          <button className="lp-btn lp-btn--ghost" type="button" onClick={() => openWorkspace('/pro')}>
            登录
          </button>
          <button className="lp-btn lp-btn--primary" type="button" onClick={() => openWorkspace('/pro')}>
            开始使用
          </button>
        </div>
      </header>

      <main>
        <section className="lp-hero" aria-labelledby="lp-hero-title">
          <div className="lp-hero__copy">
            <p className="lp-eyebrow lp-enter lp-enter--1">实时转录 · 双语翻译 · 学习空间</p>
            <h1 id="lp-hero-title" className="lp-enter lp-enter--2">
              把每一场对话，
              <br />
              变成清晰可用的文字。
            </h1>
            <p className="lp-hero__lead lp-enter lp-enter--3">
              Yufolo 专注会议、听课与访谈场景：增强级实时转录、双语同传翻译，
              以及会后的 AI 问答、摘要与知识沉淀。上课的录音还能变成技能地图和练习题，
              同一个工作台完成。
            </p>
            <div className="lp-hero__cta lp-enter lp-enter--4">
              <button className="lp-btn lp-btn--primary lp-btn--lg" type="button" onClick={() => openWorkspace('/pro')}>
                免费开始
              </button>
              <a className="lp-btn lp-btn--secondary lp-btn--lg" href="#pricing">
                查看定价
              </a>
            </div>
            <ul className="lp-hero__hints lp-enter lp-enter--5">
              <li><Icon name="check" size={13} />注册即送试用额度，无需绑卡</li>
              <li><Icon name="check" size={13} />按量计费，用多少付多少</li>
              <li><Icon name="check" size={13} />录音音频不离开本机</li>
            </ul>
          </div>

          <div className="lp-hero__panel lp-enter lp-enter--panel" aria-hidden="true">
            <LiveDemo />
          </div>
        </section>

        <section className="lp-strip lp-reveal" aria-label="产品要点">
          <div style={{ ['--lp-delay' as string]: '0ms' }}>
            <strong>秒级</strong><span>实时上屏 · 说话人分离</span>
          </div>
          <div style={{ ['--lp-delay' as string]: '70ms' }}>
            <strong>50+</strong><span>转录语言 · 增强级引擎</span>
          </div>
          <div style={{ ['--lp-delay' as string]: '140ms' }}>
            <strong>双语</strong><span>同传级阅读 · AI 翻译</span>
          </div>
          <div style={{ ['--lp-delay' as string]: '210ms' }}>
            <strong>数小时</strong><span>长会话稳定录制 · 可续录</span>
          </div>
        </section>

        <section className="lp-section" id="features" aria-labelledby="lp-features-title">
          <div className="lp-section__head lp-reveal">
            <p className="lp-eyebrow">核心能力</p>
            <h2 id="lp-features-title">主业就三件事：听清、译准、用得上</h2>
            <p>从实时现场到会后沉淀，链路打通，而不是一堆互不相关的工具。</p>
          </div>
          <div className="lp-feature-grid">
            {features.map((feature, index) => (
              <article className="lp-card lp-reveal" key={feature.title} style={{ ['--lp-delay' as string]: `${index * 80}ms` }}>
                <span className="lp-card__icon"><Icon name={feature.icon} size={20} /></span>
                <h3>{feature.title}</h3>
                <p>{feature.body}</p>
              </article>
            ))}
          </div>
        </section>

        <section className="lp-section lp-section--muted" id="study" aria-labelledby="lp-study-title">
          <div className="lp-section__head lp-reveal">
            <p className="lp-eyebrow">学习空间</p>
            <h2 id="lp-study-title">上课的录音，变成一条可以练的路线</h2>
            <p>像驾校一样：先讲解，再练习，错了看解析，下次再来。而不是把整段录音丢给你自己翻。</p>
          </div>
          <ol className="lp-study-flow lp-reveal" aria-label="学习流程">
            {studyFlow.map((item) => <li key={item}>{item}</li>)}
          </ol>
          <div className="lp-feature-grid">
            {studyCards.map((card, index) => (
              <article className="lp-card lp-reveal" key={card.title} style={{ ['--lp-delay' as string]: `${index * 80}ms` }}>
                <span className="lp-card__icon"><Icon name={card.icon} size={20} /></span>
                <h3>{card.title}</h3>
                <p>{card.body}</p>
              </article>
            ))}
          </div>
        </section>

        <section className="lp-section" aria-labelledby="lp-why-title">
          <div className="lp-section__head lp-reveal">
            <p className="lp-eyebrow">为什么选 Yufolo</p>
            <h2 id="lp-why-title">在三个不妥协的地方，我们都选了贵的那条路</h2>
            <p>因为转录的价值只取决于一件事：关键的那句话，有没有被准确留下来。</p>
          </div>
          <div className="lp-feature-grid lp-why-grid">
            {pillars.map((pillar, index) => (
              <article className="lp-card lp-reveal" key={pillar.title} style={{ ['--lp-delay' as string]: `${index * 80}ms` }}>
                <span className="lp-card__icon"><Icon name={pillar.icon} size={20} /></span>
                <h3>{pillar.title}</h3>
                <p>{pillar.body}</p>
              </article>
            ))}
          </div>
        </section>

        <section className="lp-section lp-section--muted" id="scenarios" aria-labelledby="lp-scenarios-title">
          <div className="lp-section__head lp-reveal">
            <p className="lp-eyebrow">适用场景</p>
            <h2 id="lp-scenarios-title">为真正需要「留下文字」的场合而做</h2>
          </div>
          <div className="lp-scenario-grid">
            {scenarios.map((item, index) => (
              <article className="lp-scenario lp-reveal" key={item.label} style={{ ['--lp-delay' as string]: `${index * 90}ms` }}>
                <span className="lp-scenario__label">{item.label}</span>
                <h3>{item.title}</h3>
                <p>{item.body}</p>
              </article>
            ))}
          </div>
        </section>

        <PricingSection />

        <section className="lp-section" id="how" aria-labelledby="lp-how-title">
          <div className="lp-section__head lp-reveal">
            <p className="lp-eyebrow">上手</p>
            <h2 id="lp-how-title">三步开始</h2>
          </div>
          <ol className="lp-steps">
            {steps.map((step, index) => (
              <li className="lp-reveal" key={step.n} style={{ ['--lp-delay' as string]: `${index * 90}ms` }}>
                <span className="lp-steps__n">{step.n}</span>
                <div>
                  <h3>{step.title}</h3>
                  <p>{step.body}</p>
                </div>
              </li>
            ))}
          </ol>
        </section>

        <section className="lp-section lp-section--muted" id="faq" aria-labelledby="lp-faq-title">
          <div className="lp-section__head lp-reveal">
            <p className="lp-eyebrow">常见问题</p>
            <h2 id="lp-faq-title">你可能想先知道这些</h2>
          </div>
          <div className="lp-faq lp-reveal">
            {faqs.map((item) => (
              <details className="lp-faq__item" key={item.q}>
                <summary>
                  <span>{item.q}</span>
                  <Icon name="arrow-down" size={16} />
                </summary>
                <p>{item.a}</p>
              </details>
            ))}
          </div>
        </section>

        <section className="lp-cta-band lp-reveal" aria-labelledby="lp-cta-title">
          <div>
            <h2 id="lp-cta-title">准备好记录下一场对话了吗？</h2>
            <p>进入工作台，立刻开始实时转录与翻译。</p>
          </div>
          <div className="lp-hero__cta">
            <button className="lp-btn lp-btn--primary lp-btn--lg" type="button" onClick={() => openWorkspace('/pro')}>
              免费开始
            </button>
            <button className="lp-btn lp-btn--secondary lp-btn--lg" type="button" onClick={() => openWorkspace('/pro')}>
              已有账户，登录
            </button>
          </div>
        </section>
      </main>

      <footer className="lp-footer lp-reveal">
        <div className="lp-footer__grid">
          <div className="lp-footer__about">
            <div className="lp-footer__brand">
              <span className="lp-brand__mark lp-brand__mark--sm">
                <Icon name="wave" size={16} />
              </span>
              <span>Yufolo</span>
            </div>
            <p>实时转录 · 双语翻译 · 学习空间。为真正需要留下文字的场合而做。</p>
          </div>
          <nav className="lp-footer__col" aria-label="产品">
            <strong>产品</strong>
            <a href="#features">能力</a>
            <a href="#study">学习空间</a>
            <a href="#pricing">定价</a>
          </nav>
          <nav className="lp-footer__col" aria-label="账户">
            <strong>账户</strong>
            <a href="/pro">登录</a>
            <a href="/pro">注册</a>
            <a href="#faq">常见问题</a>
          </nav>
        </div>
        <div className="lp-footer__meta">
          <span>© {new Date().getFullYear()} Yufolo by Coyume Pty Ltd</span>
          <span>音频保留在你的设备 · 文字数据可随时导出</span>
        </div>
      </footer>
    </div>
  )
}
