import { useState, useEffect, useRef } from 'react';

export function useDiffTypewriter(targetText: string) {
  const [displayedText, setDisplayedText] = useState('');
  const [isAnimating, setIsAnimating] = useState(false);
  const previousTextRef = useRef('');
  const animationFrameRef = useRef<number | null>(null);

  useEffect(() => {
    const oldText = previousTextRef.current;
    const newText = targetText;

    if (oldText === newText) {
      setIsAnimating(false);
      return;
    }

    if (animationFrameRef.current) {
      cancelAnimationFrame(animationFrameRef.current);
    }

    setIsAnimating(true);

    const animate = () => {
      let currentText = displayedText;

      // If current text is not a prefix of the target, delete characters
      if (!newText.startsWith(currentText)) {
        currentText = currentText.slice(0, -1);
      }
      // If current text is a prefix, add characters
      else if (currentText.length < newText.length) {
        const charsToAdd = newText.slice(currentText.length, currentText.length + 2);
        currentText += charsToAdd;
      }
      
      setDisplayedText(currentText);

      if (currentText !== newText) {
        animationFrameRef.current = requestAnimationFrame(animate);
      } else {
        setIsAnimating(false);
        previousTextRef.current = newText;
      }
    };

    animationFrameRef.current = requestAnimationFrame(animate);

    return () => {
      if (animationFrameRef.current) {
        cancelAnimationFrame(animationFrameRef.current);
      }
    };
  }, [targetText, displayedText]);

  return {
    displayedText,
    cursorPosition: displayedText.length,
    isAnimating,
    isDeleting: !targetText.startsWith(displayedText),
  };
}