import React from 'react';
import { useSimpleTypewriter } from '../hooks/useSimpleTypewriter';

interface SimpleStreamingTextProps {
  text: string;
  className?: string;
}

export const SimpleStreamingText: React.FC<SimpleStreamingTextProps> = ({ 
  text, 
  className = ''
}) => {
  const { displayedText, isTyping } = useSimpleTypewriter(text, 30);
  
  return (
    <span className={className}>
      {displayedText}
      {isTyping && <span className="cursor">|</span>}
    </span>
  );
};