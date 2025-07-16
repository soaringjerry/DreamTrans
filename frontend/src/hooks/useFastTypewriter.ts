import { useState, useEffect, useRef } from 'react';

export function useFastTypewriter(text: string, speed: number = 30) {
  const [displayedText, setDisplayedText] = useState('');
  const [isTyping, setIsTyping] = useState(false);
  const targetTextRef = useRef('');
  const currentIndexRef = useRef(0);
  const rafRef = useRef<number | null>(null);
  const lastTimeRef = useRef(0);

  useEffect(() => {
    // 如果目标文本没变，不做任何事
    if (text === targetTextRef.current) {
      return;
    }

    // 如果新文本更短，说明开始了新的一行，重置
    if (text.length < targetTextRef.current.length) {
      setDisplayedText('');
      currentIndexRef.current = 0;
    }

    targetTextRef.current = text;
    setIsTyping(true);

    const animate = (timestamp: number) => {
      if (!lastTimeRef.current) lastTimeRef.current = timestamp;
      
      const elapsed = timestamp - lastTimeRef.current;
      
      if (elapsed >= speed) {
        const currentIndex = currentIndexRef.current;
        const targetLength = targetTextRef.current.length;
        
        if (currentIndex < targetLength) {
          // 一次性添加多个字符以提高性能
          const charsToAdd = Math.min(3, targetLength - currentIndex);
          const newIndex = currentIndex + charsToAdd;
          
          setDisplayedText(targetTextRef.current.slice(0, newIndex));
          currentIndexRef.current = newIndex;
          lastTimeRef.current = timestamp;
          
          rafRef.current = requestAnimationFrame(animate);
        } else {
          setIsTyping(false);
          rafRef.current = null;
        }
      } else {
        rafRef.current = requestAnimationFrame(animate);
      }
    };

    // 如果已经显示的文本是目标文本的前缀，继续从那里开始
    if (text.startsWith(displayedText)) {
      currentIndexRef.current = displayedText.length;
    }

    if (currentIndexRef.current < text.length) {
      rafRef.current = requestAnimationFrame(animate);
    } else {
      setDisplayedText(text);
      setIsTyping(false);
    }

    return () => {
      if (rafRef.current) {
        cancelAnimationFrame(rafRef.current);
        rafRef.current = null;
      }
    };
  }, [text, speed, displayedText]);

  return { displayedText, isTyping };
}