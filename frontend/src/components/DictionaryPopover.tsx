import { useEffect, useRef } from 'react'
import type { DictEntry } from '../api'

type Props = {
  open: boolean
  x: number
  y: number
  word: string
  loading?: boolean
  entry?: DictEntry | null
  onClose: () => void
}

export default function DictionaryPopover({ open, x, y, word, loading, entry, onClose }: Props) {
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    const onClick = (e: MouseEvent) => {
      const el = ref.current
      if (!el) return
      if (!el.contains(e.target as Node)) onClose()
    }
    if (open) {
      document.addEventListener('keydown', onKey)
      document.addEventListener('mousedown', onClick)
    }
    return () => {
      document.removeEventListener('keydown', onKey)
      document.removeEventListener('mousedown', onClick)
    }
  }, [open, onClose])

  if (!open) return null
  return (
    <div
      ref={ref}
      style={{
        position: 'fixed',
        left: Math.min(Math.max(8, x + 8), window.innerWidth - 280),
        top: Math.min(Math.max(8, y + 8), window.innerHeight - 200),
        width: 272,
        background: 'white',
        color: 'var(--kuro)',
        border: '1px solid var(--gin)',
        borderRadius: 8,
        boxShadow: '0 8px 24px rgba(0,0,0,0.12)',
        padding: 12,
        zIndex: 9999,
      }}
    >
      <div style={{ display:'flex', justifyContent:'space-between', alignItems:'center', marginBottom:6 }}>
        <div style={{ fontWeight:700 }}>{word}</div>
        <button className="btn btn-secondary" onClick={onClose}>关闭</button>
      </div>
      {loading ? (
        <div style={{ color:'var(--hai)' }}>查询中…</div>
      ) : entry ? (
        <div>
          {(entry.phonetic || entry.pos) && (
            <div style={{ color:'var(--hai)', marginBottom:4 }}>
              {entry.phonetic ? `/${entry.phonetic}/` : ''} {entry.pos ? ` · ${entry.pos}` : ''}
            </div>
          )}
          <div style={{ whiteSpace:'pre-wrap', lineHeight:1.4 }}>{entry.definition}</div>
          {entry.extra && (
            <div style={{ marginTop:8, color:'var(--hai)', whiteSpace:'pre-wrap' }}>{entry.extra}</div>
          )}
        </div>
      ) : (
        <div style={{ color:'var(--hai)' }}>未找到该单词。</div>
      )}
    </div>
  )
}

