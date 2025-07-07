import { useState, useEffect, useRef } from 'react';

// 找到两个字符串的最长公共前缀
function findCommonPrefix(str1: string, str2: string): number {
  let i = 0;
  while (i < str1.length && i < str2.length && str1[i] === str2[i]) {
    i++;
  }
  return i;
}

// 找到两个字符串的最长公共后缀
function findCommonSuffix(str1: string, str2: string, prefixLen: number): number {
  let i = 0;
  const len1 = str1.length;
  const len2 = str2.length;
  while (i < len1 - prefixLen && i < len2 - prefixLen && 
         str1[len1 - 1 - i] === str2[len2 - 1 - i]) {
    i++;
  }
  return i;
}

export function useDiffTypewriter(targetText: string) {
  const [displayedText, setDisplayedText] = useState('');
  const [cursorPosition, setCursorPosition] = useState(0);
  const previousTextRef = useRef('');
  const rafRef = useRef<number | null>(null);
  const actionQueueRef = useRef<Array<{type: 'delete' | 'insert', count: number, text?: string}>>([]);
  const currentActionRef = useRef<{type: 'delete' | 'insert', count: number, text?: string} | null>(null);
  const lastUpdateRef = useRef(0);

  useEffect(() => {
    // 如果目标文本没变，不做任何事
    if (targetText === previousTextRef.current) {
      return;
    }

    const oldText = previousTextRef.current;
    const newText = targetText;
    
    // 找到公共前缀和后缀
    const prefixLen = findCommonPrefix(oldText, newText);
    const suffixLen = findCommonSuffix(oldText, newText, prefixLen);
    
    // 计算需要删除和插入的部分
    const deleteStart = prefixLen;
    const deleteEnd = oldText.length - suffixLen;
    const insertText = newText.substring(prefixLen, newText.length - suffixLen);
    
    // 创建操作队列
    const newActions: typeof actionQueueRef.current = [];
    
    // 如果需要删除
    if (deleteEnd > deleteStart) {
      newActions.push({
        type: 'delete',
        count: deleteEnd - deleteStart
      });
    }
    
    // 如果需要插入
    if (insertText.length > 0) {
      newActions.push({
        type: 'insert',
        count: insertText.length,
        text: insertText
      });
    }
    
    // 如果没有操作需要执行，直接更新
    if (newActions.length === 0) {
      setDisplayedText(newText);
      setCursorPosition(newText.length);
      previousTextRef.current = newText;
      return;
    }
    
    // 设置新的操作队列
    actionQueueRef.current = newActions;
    currentActionRef.current = null;
    previousTextRef.current = newText;
    
    // 设置初始光标位置
    setCursorPosition(deleteStart);

  }, [targetText]);

  useEffect(() => {
    const animate = (timestamp: number) => {
      // 控制动画速度
      if (timestamp - lastUpdateRef.current < 10) { // 10ms per update
        rafRef.current = requestAnimationFrame(animate);
        return;
      }
      lastUpdateRef.current = timestamp;

      // 如果没有当前操作，从队列中取一个
      if (!currentActionRef.current && actionQueueRef.current.length > 0) {
        currentActionRef.current = actionQueueRef.current.shift()!;
      }

      // 如果有当前操作，执行它
      if (currentActionRef.current) {
        const action = currentActionRef.current;
        const currentText = displayedText;
        const currentCursor = cursorPosition;

        if (action.type === 'delete' && action.count > 0) {
          // 删除操作 - 每次删除2个字符以加快速度
          const deleteCount = Math.min(2, action.count);
          const newText = currentText.substring(0, currentCursor - deleteCount) + 
                         currentText.substring(currentCursor);
          setDisplayedText(newText);
          setCursorPosition(currentCursor - deleteCount);
          action.count -= deleteCount;
          
          if (action.count === 0) {
            currentActionRef.current = null;
          }
        } else if (action.type === 'insert' && action.count > 0 && action.text) {
          // 插入操作 - 每次插入3个字符以加快速度
          const insertCount = Math.min(3, action.count);
          const insertText = action.text.substring(0, insertCount);
          const newText = currentText.substring(0, currentCursor) + 
                         insertText + 
                         currentText.substring(currentCursor);
          setDisplayedText(newText);
          setCursorPosition(currentCursor + insertCount);
          action.text = action.text.substring(insertCount);
          action.count -= insertCount;
          
          if (action.count === 0) {
            currentActionRef.current = null;
          }
        }
      }

      // 如果还有操作要执行，继续动画
      if (currentActionRef.current || actionQueueRef.current.length > 0) {
        rafRef.current = requestAnimationFrame(animate);
      } else {
        rafRef.current = null;
      }
    };

    // 开始动画
    if ((currentActionRef.current || actionQueueRef.current.length > 0) && !rafRef.current) {
      rafRef.current = requestAnimationFrame(animate);
    }

    return () => {
      if (rafRef.current) {
        cancelAnimationFrame(rafRef.current);
        rafRef.current = null;
      }
    };
  }, [displayedText, cursorPosition, actionQueueRef.current.length]);

  const isAnimating = rafRef.current !== null || actionQueueRef.current.length > 0 || currentActionRef.current !== null;
  const isDeleting = currentActionRef.current?.type === 'delete';

  return {
    displayedText,
    cursorPosition,
    isAnimating,
    isDeleting
  };
}