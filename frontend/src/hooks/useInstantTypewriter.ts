import { useState, useEffect, useRef } from 'react';

// 更平滑的逐字动画：
// - 使用 requestAnimationFrame，避免 setTimeout 抖动
// - 动态分块：文本越长，每帧渲染的字符数越多（但最后阶段减速）
// - 保留公共前缀，减少闪烁
export function useInstantTypewriter(text: string) {
  const [displayedText, setDisplayedText] = useState('');
  const prevTargetRef = useRef('');
  const rafRef = useRef<number | null>(null);
  const lastTsRef = useRef(0);
  const indexRef = useRef(0);

  useEffect(() => {
    if (rafRef.current) {
      cancelAnimationFrame(rafRef.current);
      rafRef.current = null;
    }

    if (text === prevTargetRef.current) return;

    // 计算公共前缀，避免整段重绘
    const a = displayedText;
    const b = text;
    let common = 0;
    const max = Math.min(a.length, b.length);
    while (common < max && a.charCodeAt(common) === b.charCodeAt(common)) common++;

    const start = Math.min(common, displayedText.length);
    if (start < displayedText.length) {
      // 删除尾部不相同部分，避免卡顿的“整段替换”感
      setDisplayedText(displayedText.slice(0, start));
    }

    indexRef.current = start;
    prevTargetRef.current = text;
    lastTsRef.current = 0;

    const animate = (ts: number) => {
      if (!lastTsRef.current) lastTsRef.current = ts;
      const elapsed = ts - lastTsRef.current;
      // 目标“每秒字符数”（动态），长度越大越快；结束时减速
      const remaining = text.length - indexRef.current;
      const baseCps = remaining > 80 ? 90 : remaining > 30 ? 70 : 50;
      const charsPerMs = baseCps / 1000;
      let step = Math.max(1, Math.floor(elapsed * charsPerMs));
      // 尾段减速，视觉更自然
      if (remaining < 12) step = Math.min(step, 2);
      if (remaining < 5) step = 1;

      if (elapsed < 8 && remaining > 0) {
        rafRef.current = requestAnimationFrame(animate);
        return;
      }

      if (indexRef.current < text.length) {
        const nextIndex = Math.min(text.length, indexRef.current + step);
        setDisplayedText(text.slice(0, nextIndex));
        indexRef.current = nextIndex;
        lastTsRef.current = ts;
        rafRef.current = requestAnimationFrame(animate);
      } else {
        rafRef.current = null;
      }
    };

    rafRef.current = requestAnimationFrame(animate);

    return () => {
      if (rafRef.current) {
        cancelAnimationFrame(rafRef.current);
        rafRef.current = null;
      }
    };
  }, [text, displayedText]);

  return displayedText;
}
