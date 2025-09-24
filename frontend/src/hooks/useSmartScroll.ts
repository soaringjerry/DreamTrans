import { useCallback, useEffect, useRef } from 'react';

/**
 * Smart auto-scroll hook that keeps the viewport pinned to the bottom while new
 * content streams in, but gracefully pauses when the user scrolls up.
 */
export function useSmartScroll<T>(
  ref: React.RefObject<HTMLElement | null>,
  dependencies: T[]
) {
  const isLockedToBottomRef = useRef(true);
  const rafRef = useRef<number | null>(null);
  const userInteractingRef = useRef(false);
  const interactionTimerRef = useRef<number | null>(null);

  const scheduleInteractionReset = useCallback(() => {
    if (interactionTimerRef.current) {
      window.clearTimeout(interactionTimerRef.current);
    }
    interactionTimerRef.current = window.setTimeout(() => {
      userInteractingRef.current = false;
      interactionTimerRef.current = null;
      const el = ref.current;
      if (!el) return;
      const threshold = Math.max(32, el.clientHeight * 0.05);
      const delta = el.scrollHeight - el.clientHeight - el.scrollTop;
      if (delta <= threshold * 1.2) {
        isLockedToBottomRef.current = true;
      }
    }, 1400);
  }, [ref]);

  // Auto-scroll when new dependencies arrive and we're locked to the bottom.
  useEffect(() => {
    const el = ref.current;
    if (!el || !isLockedToBottomRef.current) return;

    if (rafRef.current) {
      cancelAnimationFrame(rafRef.current);
      rafRef.current = null;
    }

    rafRef.current = requestAnimationFrame(() => {
      const element = ref.current;
      if (!element) return;
      const target = element.scrollHeight - element.clientHeight;
      if (target <= 0) {
        element.scrollTop = 0;
        isLockedToBottomRef.current = true;
        return;
      }

      const delta = Math.abs((element.scrollTop ?? 0) - target);
      if (delta < 1) return;

      if (typeof element.scrollTo === 'function') {
        element.scrollTo({ top: target });
      } else {
        element.scrollTop = target;
      }
      isLockedToBottomRef.current = true;
      userInteractingRef.current = false;
    });

    return () => {
      if (rafRef.current) {
        cancelAnimationFrame(rafRef.current);
        rafRef.current = null;
      }
    };
  }, [ref, dependencies]);

  // Listen for user scroll interactions.
  useEffect(() => {
    const element = ref.current;
    if (!element) return;

    const handleScroll = () => {
      const threshold = Math.max(32, element.clientHeight * 0.05);
      const delta = element.scrollHeight - element.clientHeight - element.scrollTop;
      const isAtBottom = delta < threshold;

      if (isAtBottom) {
        isLockedToBottomRef.current = true;
        userInteractingRef.current = false;
        if (interactionTimerRef.current) {
          window.clearTimeout(interactionTimerRef.current);
          interactionTimerRef.current = null;
        }
        return;
      }

      if (userInteractingRef.current) {
        isLockedToBottomRef.current = false;
        return;
      }

      if (delta <= threshold * 1.6) {
        isLockedToBottomRef.current = true;
      } else {
        isLockedToBottomRef.current = false;
      }
    };

    const unlock = () => {
      userInteractingRef.current = true;
      scheduleInteractionReset();
      if (rafRef.current) {
        cancelAnimationFrame(rafRef.current);
        rafRef.current = null;
      }
    };

    element.addEventListener('scroll', handleScroll, { passive: true });
    element.addEventListener('wheel', unlock, { passive: true });
    element.addEventListener('touchstart', unlock, { passive: true });
    element.addEventListener('mousedown', unlock, { passive: true });

    return () => {
      element.removeEventListener('scroll', handleScroll);
      element.removeEventListener('wheel', unlock);
      element.removeEventListener('touchstart', unlock);
      element.removeEventListener('mousedown', unlock);
      if (rafRef.current) {
        cancelAnimationFrame(rafRef.current);
        rafRef.current = null;
      }
      if (interactionTimerRef.current) {
        window.clearTimeout(interactionTimerRef.current);
        interactionTimerRef.current = null;
      }
    };
  }, [ref, scheduleInteractionReset]);
}
