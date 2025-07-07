import React from 'react';
import { useSmartTypewriter } from '../hooks/useSmartTypewriter';

interface SmartStreamingTextProps {
  text: string;
  isComplete?: boolean;
  className?: string;
}

export const SmartStreamingText: React.FC<SmartStreamingTextProps> = ({ 
  text, 
  isComplete = false,
  className = '' 
}) => {
  const { displayedText, isAnimating, isDeleting } = useSmartTypewriter(text, isComplete);
  
  return (
    <span className={className}>
      {displayedText}
      {isAnimating && (
        <span className={`cursor ${isDeleting ? 'deleting' : ''}`}>|</span>
      )}
    </span>
  );
};