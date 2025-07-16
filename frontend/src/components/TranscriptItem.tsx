import { memo } from 'react';
import { DiffStreamingText } from './DiffStreamingText';

interface TranscriptItemProps {
  speaker: string;
  confirmedText: string;
  partialText: string;
  typewriterEnabled: boolean;
}

export const TranscriptItem = memo(({ speaker, confirmedText, partialText, typewriterEnabled }: TranscriptItemProps) => {
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
         prevProps.typewriterEnabled === nextProps.typewriterEnabled;
});