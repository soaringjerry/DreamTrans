import { memo } from 'react';
import { FastStreamingText } from './FastStreamingText';

interface TranslationItemProps {
  speaker: string;
  startTime: number;
  content: string;
  isPartial: boolean;
}

export const TranslationItem = memo(({ speaker, startTime, content, isPartial }: TranslationItemProps) => {
  return (
    <div className="transcript-item">
      <span className="speaker-name">
        {speaker} ({startTime.toFixed(1)}s):
      </span>
      {isPartial ? (
        <FastStreamingText 
          text={content}
          className="text-content partial"
          speed={20}
        />
      ) : (
        <span className="text-content">
          {content}
        </span>
      )}
    </div>
  );
}, (prevProps, nextProps) => {
  // Only re-render if any prop changes
  return prevProps.speaker === nextProps.speaker &&
         prevProps.startTime === nextProps.startTime &&
         prevProps.content === nextProps.content &&
         prevProps.isPartial === nextProps.isPartial;
});