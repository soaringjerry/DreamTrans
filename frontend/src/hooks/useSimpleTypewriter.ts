import { useState, useEffect, useRef } from 'react';

export function useSimpleTypewriter(text: string, speed: number = 30) {
  const [displayedText, setDisplayedText] = useState('');
  const [isTyping, setIsTyping] = useState(false);
  const currentIndexRef = useRef(0);
  const targetTextRef = useRef('');
  const rafRef = useRef<number | null>(null);
  const lastTimeRef = useRef(0);

  useEffect(() => {
    // 如果文本没变，不做任何事
    if (text === targetTextRef.current) {
      return;
    }

    // 如果新文本不是以当前显示文本开头，说明需要重置
    if (!text.startsWith(displayedText)) {
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
          const charsToAdd = Math.min(2, targetLength - currentIndex);
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

    // 从当前显示的位置继续
    currentIndexRef.current = displayedText.length;

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