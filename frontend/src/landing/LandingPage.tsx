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
  { n: '01', title: '打开工作台', body: '登录账户，或按部署策略使用本地试用。' },
  { n: '02', title: '开始录音', body: '实时看到原文与译文；暂停、继续都在同一会话里。' },
  { n: '03', title: '问 AI / 导出', body: '生成摘要与行动项，或导出文本与本地音频。' },
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
            <strong>DreamTrans</strong>
            <small>实时转录 · 翻译 · AI 工作台</small>
          </span>
        </a>
        <nav className="lp-nav__links" aria-label="页面导航">
          <a href="#features">能力</a>
          <a href="#scenarios">场景</a>
          <a href="#how">如何开始</a>
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
            <p className="lp-eyebrow lp-enter lp-enter--1">主业产品 · 实时语音工作台</p>
            <h1 id="lp-hero-title" className="lp-enter lp-enter--2">
              把每一场对话，
              <br />
              变成清晰可用的文字。
            </h1>
            <p className="lp-hero__lead lp-enter lp-enter--3">
              DreamTrans 专注会议、听课与访谈场景：实时转录、双语翻译，
              以及会后的 AI 问答、摘要与知识沉淀——同一个工作台完成。
            </p>
            <div className="lp-hero__cta lp-enter lp-enter--4">
              <button
                className="lp-btn lp-btn--primary lp-btn--lg"
                type="button"
                onClick={() => openWorkspace('/pro')}
              >
                进入工作台
              </button>
              <button
                className="lp-btn lp-btn--secondary lp-btn--lg"
                type="button"
                onClick={() => openWorkspace('/?app=1')}
              >
                本地试用
              </button>
            </div>
            <p className="lp-hero__note lp-enter lp-enter--5">
              云端会话需登录 · 本地试用取决于服务器是否允许匿名模式
            </p>
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
            <strong>实时</strong><span>说话人分离转录</span>
          </div>
          <div style={{ ['--lp-delay' as string]: '70ms' }}>
            <strong>双语</strong><span>上下文 AI 翻译</span>
          </div>
          <div style={{ ['--lp-delay' as string]: '140ms' }}>
            <strong>AI</strong><span>问答 · 摘要 · 行动项</span>
          </div>
          <div style={{ ['--lp-delay' as string]: '210ms' }}>
            <strong>长会话</strong><span>可续录 · 可导出</span>
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

        <section className="lp-section lp-section--muted" id="scenarios" aria-labelledby="lp-scenarios-title">
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
              进入工作台
            </button>
            <button
              className="lp-btn lp-btn--secondary lp-btn--lg"
              type="button"
              onClick={() => openWorkspace('/?app=1')}
            >
              本地试用
            </button>
          </div>
        </section>
      </main>

      <footer className="lp-footer lp-reveal">
        <div className="lp-footer__brand">
          <span className="lp-brand__mark lp-brand__mark--sm">
            <Icon name="wave" size={16} />
          </span>
          <span>DreamTrans</span>
        </div>
        <p>实时转录 · 双语翻译 · AI 会话工作台</p>
        <div className="lp-footer__links">
          <a href="/pro">工作台</a>
          <a href="https://github.com/soaringjerry/DreamTrans" rel="noreferrer" target="_blank">
            GitHub
          </a>
        </div>
      </footer>
    </div>
  )
}
