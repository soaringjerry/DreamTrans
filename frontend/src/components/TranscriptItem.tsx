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
}

export const TranscriptItem = memo(({ speaker, confirmedText, partialText, typewriterEnabled, segments, translatedUntil }: TranscriptItemProps) => {
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
            return <span key={i} className={translated ? 'translated' : undefined}>{seg.text}</span>
          })
        ) : (
          confirmedText
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
