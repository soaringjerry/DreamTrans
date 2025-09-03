import { useState, useEffect, useRef } from 'react';

export function useDiffTypewriter(targetText: string) {
  const [displayedText, setDisplayedText] = useState('');
  const [cursorPosition, setCursorPosition] = useState(0);
  const previousTextRef = useRef('');
  const timeoutRef = useRef<number | null>(null);

  useEffect(() => {
    if (targetText === previousTextRef.current) {
      return;
    }

    const oldText = previousTextRef.current;
    const newText = targetText;
    
    // 清理之前的timeout
    if (timeoutRef.current) {
      clearTimeout(timeoutRef.current);
    }

    // 智能增量：只在文本增加时动画
    if (newText.startsWith(oldText) && oldText.length > 0) {
      // 新增部分直接显示，无需动画
      setDisplayedText(newText);
      setCursorPosition(newText.length);
    } else {
      // 文本变化较大，使用快速动画
      const charsToAnimate = newText.length;
      let currentIndex = 0;
      
      const animate = () => {
        if (currentIndex <= charsToAnimate) {
          const showChars = Math.min(currentIndex + 5, charsToAnimate); // 每次显示5个字符
          setDisplayedText(newText.substring(0, showChars));
          setCursorPosition(showChars);
          currentIndex = showChars;
          
          if (currentIndex < charsToAnimate) {
            timeoutRef.current = window.setTimeout(animate, 15); // 15ms延迟
          }
        }
      };
      
      animate();
    }

    previousTextRef.current = newText;

    return () => {
      if (timeoutRef.current) {
        clearTimeout(timeoutRef.current);
      }
    };
  }, [targetText]);

  return {
    displayedText,
    cursorPosition,
    isAnimating: false, // 简化状态
    isDeleting: false
  };
}