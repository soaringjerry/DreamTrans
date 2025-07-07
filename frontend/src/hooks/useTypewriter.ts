import { useState, useEffect, useRef } from 'react';

export function useTypewriter(text: string, speed: number = 20) {
  const [displayedText, setDisplayedText] = useState('');
  const currentIndexRef = useRef(0);
  const intervalRef = useRef<number | null>(null);
  const previousTextRef = useRef('');

  useEffect(() => {
    // If text hasn't changed, don't reset
    if (text === previousTextRef.current) {
      return;
    }

    // If new text is shorter or completely different, reset
    if (text.length < previousTextRef.current.length || !text.startsWith(displayedText)) {
      setDisplayedText('');
      currentIndexRef.current = 0;
    }

    previousTextRef.current = text;

    // Clear any existing interval
    if (intervalRef.current) {
      clearInterval(intervalRef.current);
    }

    // If we've already displayed all characters, no need to animate
    if (currentIndexRef.current >= text.length) {
      setDisplayedText(text);
      return;
    }

    // Start typing animation
    intervalRef.current = window.setInterval(() => {
      if (currentIndexRef.current < text.length) {
        setDisplayedText(text.slice(0, currentIndexRef.current + 1));
        currentIndexRef.current++;
      } else {
        if (intervalRef.current) {
          clearInterval(intervalRef.current);
          intervalRef.current = null;
        }
      }
    }, speed);

    return () => {
      if (intervalRef.current) {
        clearInterval(intervalRef.current);
      }
    };
  }, [text, speed]);

  return displayedText;
}