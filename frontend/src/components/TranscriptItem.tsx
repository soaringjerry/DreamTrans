import { memo } from 'react';
import { DiffStreamingText } from './DiffStreamingText';

interface Segment { text: string; startTime: number; endTime: number }

interface TranscriptItemProps {
  speaker: string;
  confirmedText: string;
  partialText: string;
  typewriterEnabled: boolean;
  segments?: Segment[];
  translatedUntil?: number; // seconds
  onWordClick?: (word: string, ev: React.MouseEvent) => void;
}

function tokenize(text: string): Array<{ t: string; isWord: boolean }> {
  if (!text) return []
  const re = /([A-Za-z]+(?:'[A-Za-z]+)?)/g
  const out: Array<{ t: string; isWord: boolean }> = []
  let last = 0
  let m: RegExpExecArray | null
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) out.push({ t: text.slice(last, m.index), isWord: false })
    out.push({ t: m[1], isWord: true })
    last = m.index + m[1].length
  }
  if (last < text.length) out.push({ t: text.slice(last), isWord: false })
  return out
}

export const TranscriptItem = memo(({ speaker, confirmedText, partialText, typewriterEnabled, segments, translatedUntil, onWordClick }: TranscriptItemProps) => {
  const visiblePartial = partialText.startsWith(confirmedText)
    ? partialText.substring(confirmedText.length).trimStart()
    : partialText;

  return (
    <div className="transcript-item">
      <span className="speaker-name">{speaker}:</span>
      <span className="text-content">
        {segments && segments.length > 0 && translatedUntil !== undefined ? (
          segments.map((seg, i) => {
            const epsilon = 0.5; // tolerate small time drift (500ms)
            const translated = seg.endTime <= ((translatedUntil ?? 0) + epsilon)
            const toks = tokenize(seg.text)
            return (
              <span key={i} className={translated ? 'translated' : undefined}>
                {toks.map((tk, idx) => tk.isWord ? (
                  <span
                    key={idx}
                    className="word"
                    onClick={(ev) => onWordClick && onWordClick(tk.t, ev)}
                    title="点击用AI解释该词"
                    style={{ cursor: 'pointer' }}
                  >
                    {tk.t}
                  </span>
                ) : (
                  <span key={idx}>{tk.t}</span>
                ))}
              </span>
            )
          })
        ) : (
          (() => {
            const toks = tokenize(confirmedText)
            return (
              <span>
                {toks.map((tk, idx) => tk.isWord ? (
                  <span
                    key={idx}
                    className="word"
                    onClick={(ev) => onWordClick && onWordClick(tk.t, ev)}
                    title="点击用AI解释该词"
                    style={{ cursor: 'pointer' }}
                  >
                    {tk.t}
                  </span>
                ) : (
                  <span key={idx}>{tk.t}</span>
                ))}
              </span>
            )
          })()
        )}
      </span>
      {visiblePartial && (
        typewriterEnabled ? (
          <DiffStreamingText 
            text={`${confirmedText ? ' ' : ''}${visiblePartial}`}
            className="text-content partial"
          />
        ) : (
          <span className="text-content partial">
            {confirmedText ? ' ' : ''}{visiblePartial}
            <span className="cursor">|</span>
          </span>
        )
      )}
    </div>
  );
}, (prevProps, nextProps) => {
  // Only re-render if any prop changes
  return prevProps.speaker === nextProps.speaker &&
         prevProps.confirmedText === nextProps.confirmedText &&
         prevProps.partialText === nextProps.partialText &&
         prevProps.typewriterEnabled === nextProps.typewriterEnabled &&
         prevProps.translatedUntil === nextProps.translatedUntil;
});
