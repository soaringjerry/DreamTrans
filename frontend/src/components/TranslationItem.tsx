import { memo } from 'react';
import { InstantStreamingText } from './InstantStreamingText';

interface TranslationItemProps {
  speaker: string;
  startTime: number;
  content: string;
  isPartial: boolean;
  typewriterEnabled: boolean;
}

export const TranslationItem = memo(({ speaker, startTime, content, isPartial, typewriterEnabled }: TranslationItemProps) => {
  return (
    <div className="transcript-item">
      <span className="speaker-name">
        {speaker} ({startTime.toFixed(1)}s):
      </span>
      {isPartial ? (
        typewriterEnabled ? (
          <InstantStreamingText 
            text={content}
            className="text-content partial"
          />
        ) : (
          <span className="text-content partial">
            {content}
            <span className="cursor">|</span>
          </span>
        )
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
         prevProps.isPartial === nextProps.isPartial &&
         prevProps.typewriterEnabled === nextProps.typewriterEnabled;
});