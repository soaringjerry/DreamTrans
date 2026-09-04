import { useEffect, useState } from 'react'
import { LocaleSwitch } from '../i18n/LocaleSwitch'
import { useLocale, useMessages } from '../i18n'
import {
  LEGAL_EFFECTIVE_DATE,
  legalDocument,
  type LegalKind,
} from './documents'
import '../landing/LandingPage.css'
import './LegalPage.css'

function openWorkspace(path: string) {
  window.location.assign(path)
}

/**
 * Split on the capture group so addresses land on the odd indices. Each domain
 * label must be followed by word characters, so a sentence-ending period after
 * the address stays out of the link.
 */
const EMAIL_PATTERN = /([\w.+-]+@[\w-]+(?:\.[\w-]+)+)/

/**
 * A contact address in a legal document is there to be used, so render it as a
 * mail link. The documents stay plain strings; only the page knows about markup.
 */
function withMailLinks(text: string) {
  const parts = text.split(EMAIL_PATTERN)
  if (parts.length === 1) return text
  return parts.map((part, index) => (
    index % 2 === 1
      ? <a href={`mailto:${part}`} key={index}>{part}</a>
      : part
  ))
}

/**
 * Lights the contents rail for the section being read. The band starts below
 * the sticky nav and ends short of the fold, so the entry that changes is the
 * heading the reader has actually reached rather than whatever is on screen.
 */
function useActiveSection(idKey: string): string | null {
  const [active, setActive] = useState<string | null>(null)

  useEffect(() => {
    const ids = idKey.split('|').filter(Boolean)
    const nodes = ids
      .map((id) => document.getElementById(id))
      .filter((node): node is HTMLElement => node !== null)
    if (nodes.length === 0) return

    setActive(ids[0] ?? null)
    const visible = new Map<string, boolean>()
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) visible.set(entry.target.id, entry.isIntersecting)
        // Keep the last match while scrolling through a long section whose
        // heading has already passed above the band.
        const first = ids.find((id) => visible.get(id))
        if (first) setActive(first)
      },
      { rootMargin: '-84px 0px -62% 0px', threshold: 0 },
    )
    for (const node of nodes) observer.observe(node)
    return () => observer.disconnect()
  }, [idKey])

  return active
}

export default function LegalPage({ kind }: { kind: LegalKind }) {
  const [locale] = useLocale()
  const m = useMessages()
  const legal = legalDocument(kind, locale)
  const otherKind: LegalKind = kind === 'privacy' ? 'terms' : 'privacy'
  const otherHref = otherKind === 'privacy' ? '/privacy' : '/terms'
  const otherTitle = otherKind === 'privacy' ? m.legal.privacy : m.legal.terms
  const active = useActiveSection(legal.sections.map((section) => section.id).join('|'))

  useEffect(() => {
    const previous = window.document.title
    window.document.title = `${legal.title} · ${m.common.brand}`
    return () => {
      window.document.title = previous
    }
  }, [legal.title, m.common.brand])

  return (
    <div className="lp lp--legal">
      <header className="lp-nav">
        <a className="lp-brand" href="/" title={m.landing.tagline}>
          <img
            alt={m.common.brand}
            className="lp-brand__logo"
            decoding="async"
            draggable={false}
            height={44}
            src="/brand/yufolo-logo.png"
            width={157}
          />
        </a>
        <nav className="lp-nav__links" aria-label={m.legal.navAria}>
          <a href="/privacy" aria-current={kind === 'privacy' ? 'page' : undefined}>
            {m.legal.privacy}
          </a>
          <a href="/terms" aria-current={kind === 'terms' ? 'page' : undefined}>
            {m.legal.terms}
          </a>
        </nav>
        <div className="lp-nav__actions">
          <LocaleSwitch className="lp-locale" />
          <button className="lp-btn lp-btn--ghost" type="button" onClick={() => openWorkspace('/pro')}>
            {m.landing.nav.login}
          </button>
          <button className="lp-btn lp-btn--primary" type="button" onClick={() => openWorkspace('/pro')}>
            {m.landing.nav.start}
          </button>
        </div>
      </header>

      <main className="lp-legal">
        <div className="lp-legal__head">
          <p className="lp-eyebrow">{m.legal.eyebrow}</p>
          <h1>{legal.title}</h1>
          <p className="lp-legal__meta">{m.legal.updated(LEGAL_EFFECTIVE_DATE)}</p>
          <p className="lp-legal__summary">{withMailLinks(legal.summary)}</p>
        </div>

        <div className="lp-legal__body">
          <nav className="lp-legal__toc" aria-label={m.legal.toc}>
            <strong>{m.legal.toc}</strong>
            <ol>
              {legal.sections.map((section) => (
                <li key={section.id}>
                  <a
                    aria-current={section.id === active ? 'true' : undefined}
                    href={`#${section.id}`}
                  >
                    {section.heading}
                  </a>
                </li>
              ))}
            </ol>
          </nav>

          <article className="lp-legal__doc">
            {legal.sections.map((section) => (
              <section key={section.id} id={section.id}>
                <h2>{section.heading}</h2>
                {section.blocks.map((block, index) => (
                  block.type === 'p' ? (
                    <p key={index}>{withMailLinks(block.text)}</p>
                  ) : (
                    <ul key={index}>
                      {block.items.map((item) => (
                        <li key={item}>{withMailLinks(item)}</li>
                      ))}
                    </ul>
                  )
                ))}
              </section>
            ))}

            <p className="lp-legal__also">
              <a href={otherHref}>{otherTitle}</a>
              <span aria-hidden="true"> · </span>
              <a href="/">{m.legal.backHome}</a>
            </p>
          </article>
        </div>
      </main>

      <footer className="lp-footer">
        <div className="lp-footer__meta">
          <span>{m.landing.footer.copyright(new Date().getFullYear())}</span>
          <nav className="lp-footer__legal-links" aria-label={m.legal.navAria}>
            <a href="/privacy">{m.legal.privacy}</a>
            <a href="/terms">{m.legal.terms}</a>
          </nav>
        </div>
      </footer>
    </div>
  )
}
