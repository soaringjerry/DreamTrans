import { useMemo } from 'react'

type Segment = { text: string; startTime: number; endTime: number }
type Line = { speaker: string; confirmedSegments: Segment[] }
type Translation = { speaker: string; startTime: number; content: string }

function splitEN(s: string): string[] {
  const t = (s || '').trim()
  if (!t) return []
  // split by sentence enders, keep simple
  return t.split(/(?<=[\.\?!;…])\s+/).map(x => x.trim()).filter(Boolean)
}
function splitZH(s: string): string[] {
  const t = (s || '').trim()
  if (!t) return []
  return t.split(/(?<=[。！？；…])\s*/).map(x => x.trim()).filter(Boolean)
}

function englishForRange(lines: Line[], speaker: string, s: number, e: number): string {
  const parts: string[] = []
  for (const ln of lines) {
    if (ln.speaker !== speaker) continue
    for (const seg of ln.confirmedSegments) {
      if (seg.endTime >= s && seg.startTime <= e) {
        parts.push(seg.text)
      }
    }
  }
  return parts.join(' ').replace(/\s+/g, ' ').trim()
}

function pairSentences(enList: string[], zhList: string[]): Array<{ en: string; zh: string }> {
  const pairs: Array<{ en: string; zh: string }> = []
  if (enList.length === zhList.length) {
    for (let i=0;i<enList.length;i++) pairs.push({ en: enList[i], zh: zhList[i] })
    return pairs
  }
  // fallback: greedy by ratio of lengths
  const totalEn = enList.join(' ').length || 1
  const totalZh = zhList.join(' ').length || 1
  let i=0, j=0
  while (i < enList.length || j < zhList.length) {
    let en = ''
    let zh = ''
    // accumulate minimal chunks to roughly balance lengths
    let enLen = 0, zhLen = 0
    const target = (totalEn/Math.max(1,enList.length)) / (totalZh/Math.max(1,zhList.length))
    if (i < enList.length) { en = enList[i++]; enLen = en.length }
    if (j < zhList.length) { zh = zhList[j++]; zhLen = zh.length }
    while (i < enList.length && (enLen/Math.max(1,zhLen)) < target*0.7) { en += ' ' + enList[i]; enLen += enList[i].length; i++ }
    while (j < zhList.length && (zhLen/Math.max(1,enLen)) < (1/target)*0.7) { zh += zhList[j]; zhLen += zhList[j].length; j++ }
    pairs.push({ en: en.trim(), zh: zh.trim() })
  }
  return pairs
}

export default function BilingualPanel({ lines, translations }: { lines: Line[]; translations: Translation[] }) {
  const items = useMemo(() => {
    return translations.map(t => {
      const en = englishForRange(lines, t.speaker || 'Speaker', t.startTime, (t.startTime || 0) + 3600) // end bound not precise, but range overlap handled above
      const enList = splitEN(en)
      const zhList = splitZH(t.content || '')
      const pairs = pairSentences(enList, zhList)
      return { speaker: t.speaker || 'Speaker', pairs }
    })
  }, [lines, translations])

  return (
    <div className="content-list">
      {items.map((it, idx) => (
        <div key={`bi-${idx}`} className="chat-msg assistant">
          <div className="chat-avatar">{it.speaker[0] || '讲'}</div>
          <div className="chat-bubble" style={{ width:'100%' }}>
            {it.pairs.map((p, i) => (
              <div key={`p-${i}`} style={{ marginBottom: 6 }}>
                {p.en && (<div style={{ fontSize: 12, color: 'var(--hai)' }}>{p.en}</div>)}
                {p.zh && (<div style={{ fontWeight: 600, color: 'var(--kuro)' }}>{p.zh}</div>)}
              </div>
            ))}
          </div>
        </div>
      ))}
    </div>
  )
}
