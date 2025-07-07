import { useState, useEffect, useRef } from 'react';

export function useInstantTypewriter(text: string) {
  const [displayedText, setDisplayedText] = useState('');
  const previousTextRef = useRef('');
  const timeoutRef = useRef<number | null>(null);

  useEffect(() => {
    // 清理之前的 timeout
    if (timeoutRef.current) {
      clearTimeout(timeoutRef.current);
      timeoutRef.current = null;
    }

    // 如果文本没变，不做任何事
    if (text === previousTextRef.current) {
      return;
    }

    // 如果新文本是在旧文本基础上添加的，立即显示新增部分
    if (text.startsWith(displayedText)) {
      const newChars = text.substring(displayedText.length);
      
      // 立即显示大部分新增内容
      if (newChars.length > 5) {
        // 立即显示除了最后几个字符外的所有内容
        const instantShow = text.substring(0, text.length - 3);
        setDisplayedText(instantShow);
        
        // 快速动画显示最后几个字符
        let index = instantShow.length;
        const showRest = () => {
          if (index < text.length) {
            setDisplayedText(text.substring(0, index + 1));
            index++;
            timeoutRef.current = window.setTimeout(showRest, 30);
          }
        };
        timeoutRef.current = window.setTimeout(showRest, 30);
      } else {
        // 对于短文本，快速逐字显示
        let index = displayedText.length;
        const showChar = () => {
          if (index < text.length) {
            setDisplayedText(text.substring(0, index + 1));
            index++;
            timeoutRef.current = window.setTimeout(showChar, 20);
          }
        };
        showChar();
      }
    } else {
      // 文本完全改变，重置
      setDisplayedText('');
      
      // 快速显示新文本
      if (text.length > 10) {
        // 立即显示大部分内容
        const instantShow = text.substring(0, text.length - 3);
        setDisplayedText(instantShow);
        
        // 动画显示最后几个字符
        let index = instantShow.length;
        const showRest = () => {
          if (index < text.length) {
            setDisplayedText(text.substring(0, index + 1));
            index++;
            timeoutRef.current = window.setTimeout(showRest, 30);
          }
        };
        timeoutRef.current = window.setTimeout(showRest, 30);
      } else {
        // 短文本快速显示
        let index = 0;
        const showChar = () => {
          if (index < text.length) {
            setDisplayedText(text.substring(0, index + 1));
            index++;
            timeoutRef.current = window.setTimeout(showChar, 20);
          }
        };
        showChar();
      }
    }

    previousTextRef.current = text;

    return () => {
      if (timeoutRef.current) {
        clearTimeout(timeoutRef.current);
        timeoutRef.current = null;
      }
    };
  }, [text, displayedText]);

  return displayedText;
}