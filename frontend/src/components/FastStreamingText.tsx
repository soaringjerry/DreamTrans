import React from 'react';
import { useFastTypewriter } from '../hooks/useFastTypewriter';

interface FastStreamingTextProps {
  text: string;
  className?: string;
  speed?: number;
}

export const FastStreamingText: React.FC<FastStreamingTextProps> = ({ 
  text, 
  className = '',
  speed = 30 // 30ms per update, showing 3 chars at a time = ~100 chars/second
}) => {
  const { displayedText, isTyping } = useFastTypewriter(text, speed);
  
  return (
    <span className={className}>
      {displayedText}
      {isTyping && <span className="cursor">|</span>}
    </span>
  );
};