import { useRef, useEffect } from 'react';

/**
 * Custom hook for implementing smart auto-scroll behavior.
 * Auto-scrolls to bottom when new content arrives, but pauses when user scrolls up.
 * Resumes auto-scrolling when user manually scrolls back to bottom.
 * 
 * @param ref - Reference to the scrollable container element
 * @param dependencies - Array of dependencies that trigger scroll when changed
 */
export function useSmartScroll<T>(
  ref: React.RefObject<HTMLElement | null>,
  dependencies: T[]
) {
  // Track whether the user is locked to bottom (i.e., auto-scroll is active)
  const isLockedToBottomRef = useRef(true);
  const rafRef = useRef<number | null>(null);

  // Handle auto-scrolling when dependencies change (new content arrives)
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
        return;
      }

      const delta = Math.abs((element.scrollTop ?? 0) - target);
      if (delta < 1) return;

      if (typeof element.scrollTo === 'function') {
        element.scrollTo({ top: target });
      } else {
        element.scrollTop = target;
      }
    });

    return () => {
      if (rafRef.current) {
        cancelAnimationFrame(rafRef.current);
        rafRef.current = null;
      }
    };
  }, [ref, dependencies]);

  // Set up scroll event listener to track user behavior
  useEffect(() => {
    const element = ref.current;
    if (!element) return;

    const handleScroll = () => {
      // Calculate if user is at the bottom with a flexible threshold for tolerance
      const scrollThreshold = Math.max(32, element.clientHeight * 0.05);
      const isAtBottom =
        element.scrollHeight - element.scrollTop - element.clientHeight < scrollThreshold;

      // Update the lock state based on scroll position
      isLockedToBottomRef.current = isAtBottom;
    };

    const unlock = () => {
      isLockedToBottomRef.current = false;
      if (rafRef.current) {
        cancelAnimationFrame(rafRef.current);
        rafRef.current = null;
      }
    };

    // Attach listeners
    element.addEventListener('scroll', handleScroll, { passive: true });
    element.addEventListener('wheel', unlock, { passive: true });
    element.addEventListener('touchstart', unlock, { passive: true });
    element.addEventListener('mousedown', unlock, { passive: true });

    // Clean up event listener on unmount
    return () => {
      element.removeEventListener('scroll', handleScroll);
      element.removeEventListener('wheel', unlock);
      element.removeEventListener('touchstart', unlock);
      element.removeEventListener('mousedown', unlock);
      if (rafRef.current) {
        cancelAnimationFrame(rafRef.current);
        rafRef.current = null;
      }
    };
  }, [ref]);
}
