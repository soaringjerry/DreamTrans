import { useEffect, useRef, useState } from 'react'
import { formatUSD, getPublicPricing, type PublicPlan, type PublicPricing } from '../api'
import { intlLocale, useMessages, type Messages } from '../i18n'
import { LocaleSwitch } from '../i18n/LocaleSwitch'
import { Icon, type IconName } from '../unified/components/Icon'
import { LiveDemo } from './LiveDemo'
import './LandingPage.css'

const FEATURE_ICONS: readonly IconName[] = ['mic', 'language', 'sparkles', 'cloud']
const STUDY_ICONS: readonly IconName[] = ['archive', 'map', 'message', 'language']
const PILLAR_ICONS: readonly IconName[] = ['check', 'shield', 'message']

/** Plan feature flags the catalog may carry, in display order. */
const PLAN_FEATURE_KEYS = ['premium_models', 'byok', 'batch', 'custom_prompt', 'auto_topup', 'export_ledger', 'api_access'] as const

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

function discountLabel(p: Messages['landing']['pricing'], percent: number): string | null {
  if (percent <= 0) return null
  const factor = (100 - percent) / 10
  return p.discount(new Intl.NumberFormat(intlLocale(), { maximumFractionDigits: 1 }).format(factor))
}

function planBullets(
  p: Messages['landing']['pricing'],
  plan: PublicPlan,
  trialUSD: number,
  cheapest: PublicPlan | null,
): string[] {
  const bullets: string[] = []
  const free = plan.price_usd_month <= 0
  if (free) {
    bullets.push(trialUSD > 0 ? p.trial(money(trialUSD)) : p.freeNoTrial)
    bullets.push(...p.baseIncluded)
    bullets.push(p.payg)
  } else {
    bullets.push(cheapest && cheapest.code !== plan.code ? p.includesPlan(cheapest.name) : p.allCore)
    const discount = discountLabel(p, plan.usage_discount_percent)
    if (discount) bullets.push(discount)
    for (const key of PLAN_FEATURE_KEYS) {
      if (plan.features[key]) bullets.push(p.featureFlags[key])
    }
  }
  if (plan.max_concurrent_sessions > 1) bullets.push(p.concurrent(plan.max_concurrent_sessions))
  if (plan.retention_days > 0) bullets.push(p.retention(plan.retention_days))
  if (plan.storage_gb > 0 && !free) bullets.push(p.storage(plan.storage_gb))
  return bullets
}

function yearlyNote(p: Messages['landing']['pricing'], plan: PublicPlan): string | null {
  if (plan.price_usd_month <= 0 || plan.price_usd_year <= 0) return null
  const monthsFree = 12 - plan.price_usd_year / plan.price_usd_month
  if (monthsFree >= 0.5) return p.yearlyFree(money(plan.price_usd_year), Math.round(monthsFree))
  return p.yearly(money(plan.price_usd_year))
}

function PricingSection() {
  const p = useMessages().landing.pricing
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
        <p className="lp-eyebrow">{p.eyebrow}</p>
        <h2 id="lp-pricing-title">{p.title}</h2>
        <p>{p.lead}</p>
      </div>

      {pricing && plans.length > 0 && (
        <>
          <div className={`lp-pricing-grid${plans.length >= 3 ? ' lp-pricing-grid--three' : ''}`}>
            {plans.map((plan, index) => {
              const featured = plan.code === featuredCode
              const note = yearlyNote(p, plan)
              return (
                <article
                  className={`lp-price-card lp-reveal${featured ? ' lp-price-card--featured' : ''}`}
                  key={plan.code}
                  style={{ ['--lp-delay' as string]: `${index * 90}ms` }}
                >
                  {featured && <span className="lp-price-card__badge">{p.popular}</span>}
                  <h3>{plan.name}</h3>
                  <p className="lp-price-card__price">
                    <strong>{money(plan.price_usd_month)}</strong>
                    <span>{p.perMonth}</span>
                  </p>
                  <p className="lp-price-card__tagline">
                    {note ?? (plan.price_usd_month <= 0 ? p.freeTagline : p.paidTagline)}
                  </p>
                  {plan.realtime_hour_usd > 0 && (
                    <p className="lp-price-card__hourly">
                      <span>{p.hourly}</span>
                      <strong>{p.perHour(formatUSD(plan.realtime_hour_usd))}</strong>
                    </p>
                  )}
                  <ul>
                    {planBullets(p, plan, trialUSD, cheapest).map((bullet) => (
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
                    {plan.price_usd_month <= 0 ? p.startFree : p.upgrade(plan.name)}
                  </button>
                </article>
              )
            })}
          </div>
          {tiers.length > 0 && (
            <div className="lp-topup lp-reveal" aria-label={p.topupAria}>
              {tiers.map((tier) => {
                const [before, amount, between, bonus] = p.topup(money(tier.amount_usd), tier.bonus_percent)
                return (
                  <span key={tier.amount_usd}>
                    {before}<strong>{amount}</strong>{between}<strong>{bonus}</strong>
                  </span>
                )
              })}
            </div>
          )}
          <p className="lp-pricing-note lp-reveal">
            {p.noteBase}
            {pricing.payments_enabled
              ? p.noteStripe(pricing.checkout_currency && pricing.checkout_currency !== 'usd' ? pricing.checkout_currency.toUpperCase() : '')
              : p.noteNoStripe}
          </p>
        </>
      )}

      {!pricing && !failed && (
        <p className="lp-pricing-note lp-reveal">{p.loading}</p>
      )}
      {failed && (
        <div className="lp-pricing-grid">
          <article className="lp-price-card lp-reveal">
            <h3>{p.fallbackFree}</h3>
            <p className="lp-price-card__tagline">{p.fallbackFreeTagline}</p>
            <ul>
              {p.baseIncluded.map((item) => (
                <li key={item}><Icon name="check" size={14} /><span>{item}</span></li>
              ))}
            </ul>
            <button className="lp-btn lp-btn--lg lp-btn--primary" type="button" onClick={() => openWorkspace('/pro')}>
              {p.startFree}
            </button>
          </article>
          <article className="lp-price-card lp-reveal">
            <h3>{p.fallbackMember}</h3>
            <p className="lp-price-card__tagline">{p.fallbackMemberTagline}</p>
            <button className="lp-btn lp-btn--lg lp-btn--secondary" type="button" onClick={() => openWorkspace('/pro')}>
              {p.fallbackLogin}
            </button>
          </article>
        </div>
      )}
    </section>
  )
}

export default function LandingPage() {
  const rootRef = useRevealOnScroll<HTMLDivElement>()
  const m = useMessages()
  const l = m.landing

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
            <strong>{m.common.brand}</strong>
            <small>{l.tagline}</small>
          </span>
        </a>
        <nav className="lp-nav__links" aria-label={l.nav.aria}>
          <a href="#features">{l.nav.features}</a>
          <a href="#study">{l.nav.study}</a>
          <a href="#pricing">{l.nav.pricing}</a>
          <a href="#faq">{l.nav.faq}</a>
        </nav>
        <div className="lp-nav__actions">
          <LocaleSwitch className="lp-locale" />
          <button className="lp-btn lp-btn--ghost" type="button" onClick={() => openWorkspace('/pro')}>
            {l.nav.login}
          </button>
          <button className="lp-btn lp-btn--primary" type="button" onClick={() => openWorkspace('/pro')}>
            {l.nav.start}
          </button>
        </div>
      </header>

      <main>
        <section className="lp-hero" aria-labelledby="lp-hero-title">
          <div className="lp-hero__copy">
            <p className="lp-eyebrow lp-enter lp-enter--1">{l.hero.eyebrow}</p>
            <h1 id="lp-hero-title" className="lp-enter lp-enter--2">
              {l.hero.titleA}
              <br />
              {l.hero.titleB}
            </h1>
            <p className="lp-hero__lead lp-enter lp-enter--3">{l.hero.lead}</p>
            <div className="lp-hero__cta lp-enter lp-enter--4">
              <button className="lp-btn lp-btn--primary lp-btn--lg" type="button" onClick={() => openWorkspace('/pro')}>
                {l.hero.start}
              </button>
              <a className="lp-btn lp-btn--secondary lp-btn--lg" href="#pricing">
                {l.hero.pricing}
              </a>
            </div>
            <ul className="lp-hero__hints lp-enter lp-enter--5">
              {l.hero.hints.map((hint) => (
                <li key={hint}><Icon name="check" size={13} />{hint}</li>
              ))}
            </ul>
          </div>

          <div className="lp-hero__panel lp-enter lp-enter--panel" aria-hidden="true">
            <LiveDemo />
          </div>
        </section>

        <section className="lp-strip lp-reveal" aria-label={l.strip.aria}>
          {l.strip.items.map((item, index) => (
            <div key={item.label} style={{ ['--lp-delay' as string]: `${index * 70}ms` }}>
              <strong>{item.value}</strong><span>{item.label}</span>
            </div>
          ))}
        </section>

        <section className="lp-section" id="features" aria-labelledby="lp-features-title">
          <div className="lp-section__head lp-reveal">
            <p className="lp-eyebrow">{l.features.eyebrow}</p>
            <h2 id="lp-features-title">{l.features.title}</h2>
            <p>{l.features.lead}</p>
          </div>
          <div className="lp-feature-grid">
            {l.features.items.map((feature, index) => (
              <article className="lp-card lp-reveal" key={feature.title} style={{ ['--lp-delay' as string]: `${index * 80}ms` }}>
                <span className="lp-card__icon"><Icon name={FEATURE_ICONS[index] ?? 'check'} size={20} /></span>
                <h3>{feature.title}</h3>
                <p>{feature.body}</p>
              </article>
            ))}
          </div>
        </section>

        <section className="lp-section lp-section--muted" id="study" aria-labelledby="lp-study-title">
          <div className="lp-section__head lp-reveal">
            <p className="lp-eyebrow">{l.study.eyebrow}</p>
            <h2 id="lp-study-title">{l.study.title}</h2>
            <p>{l.study.lead}</p>
          </div>
          <ol className="lp-study-flow lp-reveal" aria-label={l.study.flowAria}>
            {l.study.flow.map((item) => <li key={item}>{item}</li>)}
          </ol>
          <div className="lp-feature-grid">
            {l.study.items.map((card, index) => (
              <article className="lp-card lp-reveal" key={card.title} style={{ ['--lp-delay' as string]: `${index * 80}ms` }}>
                <span className="lp-card__icon"><Icon name={STUDY_ICONS[index] ?? 'check'} size={20} /></span>
                <h3>{card.title}</h3>
                <p>{card.body}</p>
              </article>
            ))}
          </div>
        </section>

        <section className="lp-section" aria-labelledby="lp-why-title">
          <div className="lp-section__head lp-reveal">
            <p className="lp-eyebrow">{l.why.eyebrow}</p>
            <h2 id="lp-why-title">{l.why.title}</h2>
            <p>{l.why.lead}</p>
          </div>
          <div className="lp-feature-grid lp-why-grid">
            {l.why.items.map((pillar, index) => (
              <article className="lp-card lp-reveal" key={pillar.title} style={{ ['--lp-delay' as string]: `${index * 80}ms` }}>
                <span className="lp-card__icon"><Icon name={PILLAR_ICONS[index] ?? 'check'} size={20} /></span>
                <h3>{pillar.title}</h3>
                <p>{pillar.body}</p>
              </article>
            ))}
          </div>
        </section>

        <section className="lp-section lp-section--muted" id="scenarios" aria-labelledby="lp-scenarios-title">
          <div className="lp-section__head lp-reveal">
            <p className="lp-eyebrow">{l.scenarios.eyebrow}</p>
            <h2 id="lp-scenarios-title">{l.scenarios.title}</h2>
          </div>
          <div className="lp-scenario-grid">
            {l.scenarios.items.map((item, index) => (
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
            <p className="lp-eyebrow">{l.steps.eyebrow}</p>
            <h2 id="lp-how-title">{l.steps.title}</h2>
          </div>
          <ol className="lp-steps">
            {l.steps.items.map((step, index) => (
              <li className="lp-reveal" key={step.title} style={{ ['--lp-delay' as string]: `${index * 90}ms` }}>
                <span className="lp-steps__n">{String(index + 1).padStart(2, '0')}</span>
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
            <p className="lp-eyebrow">{l.faq.eyebrow}</p>
            <h2 id="lp-faq-title">{l.faq.title}</h2>
          </div>
          <div className="lp-faq lp-reveal">
            {l.faq.items.map((item) => (
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
            <h2 id="lp-cta-title">{l.cta.title}</h2>
            <p>{l.cta.lead}</p>
          </div>
          <div className="lp-hero__cta">
            <button className="lp-btn lp-btn--primary lp-btn--lg" type="button" onClick={() => openWorkspace('/pro')}>
              {l.cta.start}
            </button>
            <button className="lp-btn lp-btn--secondary lp-btn--lg" type="button" onClick={() => openWorkspace('/pro')}>
              {l.cta.login}
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
              <span>{m.common.brand}</span>
            </div>
            <p>{l.footer.about}</p>
          </div>
          <nav className="lp-footer__col" aria-label={l.footer.product}>
            <strong>{l.footer.product}</strong>
            <a href="#features">{l.nav.features}</a>
            <a href="#study">{l.nav.study}</a>
            <a href="#pricing">{l.nav.pricing}</a>
          </nav>
          <nav className="lp-footer__col" aria-label={l.footer.account}>
            <strong>{l.footer.account}</strong>
            <a href="/pro">{m.common.login}</a>
            <a href="/pro">{m.common.register}</a>
            <a href="#faq">{l.nav.faq}</a>
          </nav>
        </div>
        <div className="lp-footer__meta">
          <span>{l.footer.copyright(new Date().getFullYear())}</span>
          <span>{l.footer.dataNote}</span>
        </div>
      </footer>
    </div>
  )
}
