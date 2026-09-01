import type { MoodleConfig, MoodleContext, SesskeySource } from '../shared/types'

// Reads what Moodle's own front end already put on the page. The sesskey is
// only ever used for same-origin requests from this tab; it never leaves it.

function extractBalancedObject(text: string, start: number): string | null {
  let depth = 0
  let inString = false
  let escaped = false
  for (let index = start; index < text.length; index += 1) {
    const char = text[index]
    if (inString) {
      if (escaped) escaped = false
      else if (char === '\\') escaped = true
      else if (char === '"') inString = false
      continue
    }
    if (char === '"') inString = true
    else if (char === '{') depth += 1
    else if (char === '}') {
      depth -= 1
      if (depth === 0) return text.slice(start, index + 1)
    }
  }
  return null
}

/** `M.cfg = {...};` as Moodle prints it in an inline script. */
export function parseMoodleConfig(doc: Document): Partial<MoodleConfig> | null {
  for (const script of Array.from(doc.scripts)) {
    const text = script.textContent ?? ''
    const marker = text.indexOf('M.cfg = ')
    if (marker < 0) continue
    const objectStart = text.indexOf('{', marker)
    if (objectStart < 0) continue
    const json = extractBalancedObject(text, objectStart)
    if (!json) continue
    try {
      const parsed = JSON.parse(json) as Record<string, unknown>
      return {
        wwwroot: typeof parsed.wwwroot === 'string' ? parsed.wwwroot : undefined,
        sesskey: typeof parsed.sesskey === 'string' ? parsed.sesskey : undefined,
        courseId: typeof parsed.courseId === 'number' ? parsed.courseId : Number(parsed.courseId) || undefined,
        userId: typeof parsed.userId === 'number' ? parsed.userId : undefined,
        theme: typeof parsed.theme === 'string' ? parsed.theme : undefined,
        release: typeof parsed.release === 'string' ? parsed.release : undefined,
        version: typeof parsed.version === 'string' ? parsed.version : undefined,
      } as Partial<MoodleConfig>
    } catch {
      continue
    }
  }
  return null
}

export function findSesskey(doc: Document, cfg: Partial<MoodleConfig> | null): { sesskey: string; source: SesskeySource } {
  if (cfg?.sesskey) return { sesskey: cfg.sesskey, source: 'M.cfg' }
  const input = doc.querySelector<HTMLInputElement>('input[name="sesskey"]')
  if (input?.value) return { sesskey: input.value, source: 'input' }
  for (const anchor of Array.from(doc.querySelectorAll<HTMLAnchorElement>('a[href*="sesskey="]'))) {
    const match = anchor.href.match(/[?&]sesskey=([A-Za-z0-9]+)/)
    if (match) return { sesskey: match[1], source: 'link' }
  }
  return { sesskey: '', source: 'none' }
}

function courseIdFromUrl(url: string): number {
  const match = url.match(/\/course\/(?:view|resources)\.php\?(?:.*&)?id=(\d+)/)
  return match ? Number(match[1]) : 0
}

function readCourseNames(doc: Document): { name: string; shortname: string } {
  const header = doc.querySelector('.page-header-headings h1, #page-header h1, h1')
  const name = header?.textContent?.trim() ?? doc.title.replace(/\s*[|–-]\s*.*$/, '').trim()
  // Breadcrumbs usually carry the short name (PSY2041) as the course link.
  const crumb = doc.querySelector<HTMLAnchorElement>('.breadcrumb a[href*="/course/view.php"], nav[aria-label*="Navigation"] a[href*="/course/view.php"]')
  const shortname = crumb?.textContent?.trim() || name.match(/^([A-Z]{2,5}\d{3,5}[A-Z]?)\b/)?.[1] || ''
  return { name, shortname }
}

export function readMoodleContext(doc: Document, location: Location): MoodleContext {
  const cfg = parseMoodleConfig(doc)
  const { sesskey, source } = findSesskey(doc, cfg)
  const wwwroot = cfg?.wwwroot || location.origin
  const courseId = cfg?.courseId || courseIdFromUrl(location.href)
  const names = readCourseNames(doc)
  return {
    host: location.host,
    wwwroot,
    pageUrl: location.href,
    courseId,
    courseName: names.name,
    shortname: names.shortname,
    sesskey,
    sesskeySource: source,
    userId: cfg?.userId,
    theme: cfg?.theme,
    release: cfg?.release ?? cfg?.version,
    onCoursePage: /\/course\/(view|resources)\.php/.test(location.pathname) && courseId > 0,
  }
}
