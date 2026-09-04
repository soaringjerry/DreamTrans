import { useEffect, useRef, useState } from 'react'
import { Icon } from '../unified/components/Icon'
import './LandingPage.css'

const features = [
  {
    icon: 'mic' as const,
    title: '实时转录',
    body: '说话人分离、低延迟上屏。长会议也能稳定记录，不必事后靠记忆补笔记。',
  },
  {
    icon: 'language' as const,
    title: '双语同传翻译',
    body: '上下文感知的 AI 翻译，面向会议口语润色；也可切换低延迟机翻作兜底。',
  },
  {
    icon: 'sparkles' as const,
    title: 'AI 问答与沉淀',
    body: '对着整场录音问结论、生成摘要、笔记与行动项，把对话变成可检索的资产。',
  },
  {
    icon: 'cloud' as const,
    title: '云端工作台',
    body: '登录后会话上云、桌面手机同一套界面；导出原文、译文或双语文本。',
  },
]

const scenarios = [
  {
    label: '跨国会议',
    title: '边听边懂，会后有据可查',
    body: '实时双语阅读模式，行动项与结论可一键生成，减少会后整理时间。',
  },
  {
    label: '听课学习',
    title: '一门课，一份可问的知识库',
    body: '按课程挂资料、记课堂转录；课后按问题检索相关片段，而不是翻整段录音。',
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
  { n: '03', title: '问 AI / 导出', body: '生成摘要与行动项，或导出文本与本地音频。' },
]

const pillars = [
  {
    icon: 'check' as const,
    title: '准确性优先',
    body: '只用增强级识别引擎，不设降级省钱档。专业词汇、多口音、多说话人的真实会议里，差的那几个词往往就是结论本身。',
  },
  {
    icon: 'shield' as const,
    title: '数据自主',
    body: '录音音频只保存在你的设备上，云端只同步文字。转录与译文随时可以导出为文本，删除会话时云端副本一并删除。',
  },
  {
    icon: 'message' as const,
    title: '计费透明',
    body: '美元钱包按秒、按 token 结算：每次调用先按上限预留，完成后按实际用量结算并退回差额。每一笔都有流水可查。',
  },
]

const pricingPlans = [
  {
    code: 'starter',
    name: '按量使用',
    price: '$0',
    period: '/ 月',
    tagline: '注册即送试用额度，充值即用',
    features: [
      '实时转录 · 说话人分离',
      '双语同传翻译',
      'AI 问答、摘要与行动项',
      '按量计费，用多少付多少',
      '充值余额永不过期',
      '云端会话保留 30 天',
    ],
    cta: '免费开始',
    featured: false,
  },
  {
    code: 'pro',
    name: 'Pro 会员',
    price: '$6',
    period: '/ 月',
    tagline: '按年 $60，相当于免两个月',
    features: [
      '包含按量版全部能力',
      '全部用量 8 折结算',
      '高级 AI 模型',
      '自带 API Key（BYOK）与批量转写',
      '自定义提示词 · 余额自动充值',
      '双路并发转录 · 云端保留 365 天',
    ],
    cta: '升级 Pro',
    featured: true,
  },
]

const faqs = [
  {
    q: '怎么收费？',
    a: '按实际用量从美元钱包扣费：转录按音频时长、AI 按 token。每次调用先按估算上限预留，完成后按真实用量结算并退回差额，账单里每一笔都可以核对。',
  },
  {
    q: '开始使用需要绑卡吗？',
    a: '不需要。注册免费并附送试用额度；用完后可以在线充值，充值余额永不过期。Pro 会员按月或按年订阅，随时可取消。',
  },
  {
    q: '我的录音存在哪里？',
    a: '音频只保存在你自己的设备上，云端只同步转录与翻译文字。你也可以完全在本地使用，不上传任何内容。',
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
  {
    q: '注册后马上就能用吗？',
    a: '注册后会收到一封验证邮件，点击链接即可激活账户并获得试用额度。没有收到的话，可以在登录页重新发送。',
  },
]

type MockLine =
  | {
      kind: 'speech'
      speaker: string
      en: string
      zh: string
    }
  | {
      kind: 'ai'
      label: string
    }

const mockFeed: MockLine[] = [
  {
    kind: 'speech',
    speaker: 'Speaker A',
    en: 'We should finalize the rollout checklist before Friday.',
    zh: '我们最好在周五前敲定上线检查清单。',
  },
  {
    kind: 'speech',
    speaker: 'Speaker B',
    en: 'Agreed. Can you also summarize the open risks?',
    zh: '没问题。你也可以把未决风险再总结一下吗？',
  },
  {
    kind: 'ai',
    label: 'AI · 已生成 3 条行动项',
  },
  {
    kind: 'speech',
    speaker: 'Speaker A',
    en: 'Latency looks fine on the bilingual feed.',
    zh: '双语流的延迟看起来没问题。',
  },
  {
    kind: 'speech',
    speaker: 'Speaker B',
    en: 'Great — export the notes after the call.',
    zh: '好，会后把笔记导出一份。',
  },
  {
    kind: 'ai',
    label: 'AI · 摘要已就绪',
  },
  {
    kind: 'speech',
    speaker: 'Speaker A',
    en: 'I will attach the transcript to the workspace.',
    zh: '我会把转录挂到工作台里。',
  },
  {
    kind: 'speech',
    speaker: 'Speaker B',
    en: 'Perfect. Let us review action items next.',
    zh: '很好，接下来过一下行动项。',
  },
]

function openWorkspace(path: string) {
  window.location.assign(path)
}

function prefersReducedMotion(): boolean {
  return window.matchMedia('(prefers-reduced-motion: reduce)').matches
}

function formatTimer(totalSeconds: number): string {
  const minutes = Math.floor(totalSeconds / 60)
  const seconds = totalSeconds % 60
  return `${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`
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

    const nodes = Array.from(root.querySelectorAll<HTMLElement>('.lp-reveal'))
    if (nodes.length === 0) return

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
  }, [])

  return rootRef
}

function useLiveTimer(startSeconds = 12 * 60 + 48) {
  const [seconds, setSeconds] = useState(startSeconds)

  useEffect(() => {
    if (prefersReducedMotion()) return
    const tick = window.setInterval(() => {
      setSeconds((value) => value + 1)
    }, 1000)
    return () => window.clearInterval(tick)
  }, [])

  return seconds
}

function MockFeedRow({ line }: { line: MockLine }) {
  if (line.kind === 'ai') {
    return (
      <div className="lp-mock__ai">
        <Icon name="sparkles" size={14} />
        <span>{line.label}</span>
      </div>
    )
  }

  return (
    <div className="lp-mock__row">
      <span className="lp-mock__speaker">{line.speaker}</span>
      <p className="lp-mock__en">{line.en}</p>
      <p className="lp-mock__zh">{line.zh}</p>
    </div>
  )
}

export default function LandingPage() {
  const rootRef = useRevealOnScroll<HTMLDivElement>()
  const seconds = useLiveTimer()

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
            <small>实时转录 · 翻译 · AI 工作台</small>
          </span>
        </a>
        <nav className="lp-nav__links" aria-label="页面导航">
          <a href="#features">能力</a>
          <a href="#scenarios">场景</a>
          <a href="#pricing">定价</a>
          <a href="#faq">常见问题</a>
        </nav>
        <div className="lp-nav__actions">
          <button
            className="lp-btn lp-btn--ghost"
            type="button"
            onClick={() => openWorkspace('/pro')}
          >
            登录
          </button>
          <button
            className="lp-btn lp-btn--primary"
            type="button"
            onClick={() => openWorkspace('/pro')}
          >
            开始使用
          </button>
        </div>
      </header>

      <main>
        <section className="lp-hero" aria-labelledby="lp-hero-title">
          <div className="lp-hero__copy">
            <p className="lp-eyebrow lp-enter lp-enter--1">实时语音工作台 · 面向真实会议</p>
            <h1 id="lp-hero-title" className="lp-enter lp-enter--2">
              把每一场对话，
              <br />
              变成清晰可用的文字。
            </h1>
            <p className="lp-hero__lead lp-enter lp-enter--3">
              Yufolo 专注会议、听课与访谈场景：增强级实时转录、双语同传翻译，
              以及会后的 AI 问答、摘要与知识沉淀，同一个工作台完成。
            </p>
            <div className="lp-hero__cta lp-enter lp-enter--4">
              <button
                className="lp-btn lp-btn--primary lp-btn--lg"
                type="button"
                onClick={() => openWorkspace('/pro')}
              >
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
            <div className="lp-mock">
              <div className="lp-mock__bar">
                <span className="lp-mock__dot" />
                <span>直播中 · 双语</span>
                <span className="lp-mock__timer">{formatTimer(seconds)}</span>
              </div>
              <div className="lp-mock__feed">
                <div className="lp-mock__track">
                  {/* Two identical sequences → translateY(-50%) loops without a jump. */}
                  {[0, 1].map((copy) => (
                    <div className="lp-mock__seq" key={copy}>
                      {mockFeed.map((line, index) => (
                        <MockFeedRow
                          key={`${copy}-${line.kind}-${index}`}
                          line={line}
                        />
                      ))}
                    </div>
                  ))}
                </div>
              </div>
            </div>
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
              <article
                className="lp-card lp-reveal"
                key={feature.title}
                style={{ ['--lp-delay' as string]: `${index * 80}ms` }}
              >
                <span className="lp-card__icon">
                  <Icon name={feature.icon} size={20} />
                </span>
                <h3>{feature.title}</h3>
                <p>{feature.body}</p>
              </article>
            ))}
          </div>
        </section>

        <section className="lp-section lp-section--muted" aria-labelledby="lp-why-title">
          <div className="lp-section__head lp-reveal">
            <p className="lp-eyebrow">为什么选 Yufolo</p>
            <h2 id="lp-why-title">在三个不妥协的地方，我们都选了贵的那条路</h2>
            <p>因为转录的价值只取决于一件事：关键的那句话，有没有被准确留下来。</p>
          </div>
          <div className="lp-feature-grid lp-why-grid">
            {pillars.map((pillar, index) => (
              <article
                className="lp-card lp-reveal"
                key={pillar.title}
                style={{ ['--lp-delay' as string]: `${index * 80}ms` }}
              >
                <span className="lp-card__icon">
                  <Icon name={pillar.icon} size={20} />
                </span>
                <h3>{pillar.title}</h3>
                <p>{pillar.body}</p>
              </article>
            ))}
          </div>
        </section>

        <section className="lp-section" id="scenarios" aria-labelledby="lp-scenarios-title">
          <div className="lp-section__head lp-reveal">
            <p className="lp-eyebrow">适用场景</p>
            <h2 id="lp-scenarios-title">为真正需要「留下文字」的场合而做</h2>
          </div>
          <div className="lp-scenario-grid">
            {scenarios.map((item, index) => (
              <article
                className="lp-scenario lp-reveal"
                key={item.label}
                style={{ ['--lp-delay' as string]: `${index * 90}ms` }}
              >
                <span className="lp-scenario__label">{item.label}</span>
                <h3>{item.title}</h3>
                <p>{item.body}</p>
              </article>
            ))}
          </div>
        </section>

        <section className="lp-section lp-section--muted" id="pricing" aria-labelledby="lp-pricing-title">
          <div className="lp-section__head lp-reveal">
            <p className="lp-eyebrow">定价</p>
            <h2 id="lp-pricing-title">从免费试用开始，按你的用量付费</h2>
            <p>没有按月清零的“套餐小时数”：充值余额永不过期，会员只提供折扣与高级能力。</p>
          </div>
          <div className="lp-pricing-grid">
            {pricingPlans.map((plan, index) => (
              <article
                className={`lp-price-card lp-reveal${plan.featured ? ' lp-price-card--featured' : ''}`}
                key={plan.code}
                style={{ ['--lp-delay' as string]: `${index * 90}ms` }}
              >
                {plan.featured && <span className="lp-price-card__badge">最受欢迎</span>}
                <h3>{plan.name}</h3>
                <p className="lp-price-card__price">
                  <strong>{plan.price}</strong>
                  <span>{plan.period}</span>
                </p>
                <p className="lp-price-card__tagline">{plan.tagline}</p>
                <ul>
                  {plan.features.map((feature) => (
                    <li key={feature}>
                      <Icon name="check" size={14} />
                      <span>{feature}</span>
                    </li>
                  ))}
                </ul>
                <button
                  className={`lp-btn lp-btn--lg ${plan.featured ? 'lp-btn--primary' : 'lp-btn--secondary'}`}
                  type="button"
                  onClick={() => openWorkspace('/pro')}
                >
                  {plan.cta}
                </button>
              </article>
            ))}
          </div>
          <p className="lp-pricing-note lp-reveal">
            价格以工作台内实时报价为准；支付由 Stripe 处理，会员随时可取消。
          </p>
        </section>

        <section className="lp-section" id="how" aria-labelledby="lp-how-title">
          <div className="lp-section__head lp-reveal">
            <p className="lp-eyebrow">上手</p>
            <h2 id="lp-how-title">三步开始</h2>
          </div>
          <ol className="lp-steps">
            {steps.map((step, index) => (
              <li
                className="lp-reveal"
                key={step.n}
                style={{ ['--lp-delay' as string]: `${index * 90}ms` }}
              >
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
            <button
              className="lp-btn lp-btn--primary lp-btn--lg"
              type="button"
              onClick={() => openWorkspace('/pro')}
            >
              免费开始
            </button>
            <button
              className="lp-btn lp-btn--secondary lp-btn--lg"
              type="button"
              onClick={() => openWorkspace('/pro')}
            >
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
            <p>实时转录 · 双语翻译 · AI 会话工作台。为真正需要留下文字的场合而做。</p>
          </div>
          <nav className="lp-footer__col" aria-label="产品">
            <strong>产品</strong>
            <a href="#features">能力</a>
            <a href="#scenarios">场景</a>
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
          <span>© {new Date().getFullYear()} Yufolo · CoYume Labs</span>
          <span>音频保留在你的设备 · 文字数据可随时导出</span>
        </div>
      </footer>
    </div>
  )
}
