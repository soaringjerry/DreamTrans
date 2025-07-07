import React from 'react';
import { useInstantTypewriter } from '../hooks/useInstantTypewriter';

interface InstantStreamingTextProps {
  text: string;
  className?: string;
}

export const InstantStreamingText: React.FC<InstantStreamingTextProps> = ({ 
  text, 
  className = ''
}) => {
  const displayedText = useInstantTypewriter(text);
  const isTyping = displayedText.length < text.length;
  
  return (
    <span className={className}>
      {displayedText}
      {isTyping && <span className="cursor">|</span>}
    </span>
  );
};