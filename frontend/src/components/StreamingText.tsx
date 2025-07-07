import React from 'react';
import { useTypewriter } from '../hooks/useTypewriter';

interface StreamingTextProps {
  text: string;
  speed?: number;
  className?: string;
}

export const StreamingText: React.FC<StreamingTextProps> = ({ text, speed = 20, className = '' }) => {
  const displayedText = useTypewriter(text, speed);
  
  return (
    <span className={className}>
      {displayedText}
      {displayedText.length < text.length && (
        <span className="cursor">|</span>
      )}
    </span>
  );
};