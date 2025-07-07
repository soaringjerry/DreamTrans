import { memo } from 'react';
import { DiffStreamingText } from './DiffStreamingText';

interface TranscriptItemProps {
  speaker: string;
  confirmedText: string;
  partialText: string;
}

export const TranscriptItem = memo(({ speaker, confirmedText, partialText }: TranscriptItemProps) => {
  const visiblePartial = partialText.startsWith(confirmedText)
    ? partialText.substring(confirmedText.length).trimStart()
    : partialText;

  return (
    <div className="transcript-item">
      <span className="speaker-name">{speaker}:</span>
      <span className="text-content">
        {confirmedText}
      </span>
      {visiblePartial && (
        <DiffStreamingText 
          text={`${confirmedText ? ' ' : ''}${visiblePartial}`}
          className="text-content partial"
        />
      )}
    </div>
  );
}, (prevProps, nextProps) => {
  // Only re-render if speaker, confirmed text, or partial text changes
  return prevProps.speaker === nextProps.speaker &&
         prevProps.confirmedText === nextProps.confirmedText &&
         prevProps.partialText === nextProps.partialText;
});