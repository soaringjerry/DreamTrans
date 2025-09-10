import React from 'react'

// Minimal, dependency-free Markdown renderer for chat bubbles (safe subset)
// Supports: headings, paragraphs, lists, inline code, code fences, bold/italic, links

type Node = { type: 'p'|'h'|'ul'|'ol'|'pre'; level?: number; items?: string[]; text?: string; code?: string }

// keep for future sanitization if needed

function inlineRender(line: string): React.ReactNode[] {
  const out: React.ReactNode[] = []
  let s = line
  // links [text](url)
  const linkRe = /\[([^\]]+)\]\(([^)]+)\)/g
  let last = 0; let m: RegExpExecArray | null
  const pushText = (t: string) => {
    // inline code `code`
    const parts = t.split(/(`[^`]+`)/g)
    for (const part of parts) {
      if (part.startsWith('`') && part.endsWith('`')) {
        out.push(<code key={out.length}>{part.slice(1,-1)}</code>)
      } else {
        // bold **text** and italic *text*
        let rem = part
        const boldIt = rem.split(/(\*\*[^*]+\*\*|\*[^*]+\*)/g)
        for (const bi of boldIt) {
          if (bi.startsWith('**') && bi.endsWith('**')) out.push(<strong key={out.length}>{bi.slice(2,-2)}</strong>)
          else if (bi.startsWith('*') && bi.endsWith('*')) out.push(<em key={out.length}>{bi.slice(1,-1)}</em>)
          else out.push(bi)
        }
      }
    }
  }
  while ((m = linkRe.exec(s)) !== null) {
    if (m.index > last) pushText(s.slice(last, m.index))
    out.push(<a key={out.length} href={m[2]} target="_blank" rel="noreferrer noopener">{m[1]}</a>)
    last = m.index + m[0].length
  }
  if (last < s.length) pushText(s.slice(last))
  return out
}

function parse(md: string): Node[] {
  const lines = (md||'').split(/\r?\n/)
  const out: Node[] = []
  let inCode = false; let codeBuf: string[] = []
  let listBuf: string[] = []; let listType: 'ul'|'ol'|null = null
  // paragraph flush reserved for future; currently we directly push p when needed
  const flushList = () => { if (listBuf.length) { out.push({ type: listType||'ul', items: listBuf.slice() }); listBuf = []; listType=null } }
  const flushCode = () => { if (codeBuf.length) { out.push({ type:'pre', code: codeBuf.join('\n') }); codeBuf = [] } }
  for (const raw of lines) {
    const line = raw.replace(/\t/g,'    ')
    if (line.startsWith('```')) {
      if (!inCode) { inCode = true; flushList() } else { inCode=false; flushCode() }
      continue
    }
    if (inCode) { codeBuf.push(line); continue }
    const h = line.match(/^(#{1,6})\s+(.*)$/)
    if (h) { flushList(); out.push({ type:'h', level: h[1].length, text: h[2] }); continue }
    const ul = line.match(/^\s*[-*]\s+(.*)$/)
    if (ul) { if (listType==='ol') flushList(); listType='ul'; listBuf.push(ul[1]); continue }
    const ol = line.match(/^\s*\d+\.\s+(.*)$/)
    if (ol) { if (listType==='ul') flushList(); listType='ol'; listBuf.push(ol[1]); continue }
    // blank line flush list
    if (!line.trim()) { flushList(); continue }
    flushList(); out.push({ type:'p', text: line })
  }
  flushList(); flushCode()
  return out
}

export default function MarkdownView({ text }: { text: string }) {
  const nodes = parse(text)
  return (
    <div className="markdown">
      {nodes.map((n, i) => {
        if (n.type==='h') {
          const Tag = `h${Math.min(6, Math.max(1, n.level||1))}` as any
          return <Tag key={i}>{inlineRender(n.text||'')}</Tag>
        }
        if (n.type==='ul') return (
          <ul key={i}>
            {(n.items||[]).map((it, j)=>(<li key={j}>{inlineRender(it)}</li>))}
          </ul>
        )
        if (n.type==='ol') return (
          <ol key={i}>
            {(n.items||[]).map((it, j)=>(<li key={j}>{inlineRender(it)}</li>))}
          </ol>
        )
        if (n.type==='pre') return (
          <pre key={i}><code>{n.code}</code></pre>
        )
        return <p key={i}>{inlineRender(n.text||'')}</p>
      })}
    </div>
  )
}
