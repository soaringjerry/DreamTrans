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

  // Handle auto-scrolling when dependencies change (new content arrives)
  useEffect(() => {
    if (ref.current && isLockedToBottomRef.current) {
      // Only scroll if user is locked to bottom
      ref.current.scrollTop = ref.current.scrollHeight;
    }
  }, [ref, dependencies]);

  // Set up scroll event listener to track user behavior
  useEffect(() => {
    const element = ref.current;
    if (!element) return;

    const handleScroll = () => {
      // Calculate if user is at the bottom with a small threshold for tolerance
      const scrollThreshold = 10; // 10px tolerance
      const isAtBottom = 
        element.scrollHeight - element.scrollTop - element.clientHeight < scrollThreshold;
      
      // Update the lock state based on scroll position
      isLockedToBottomRef.current = isAtBottom;
    };

    // Attach scroll event listener
    element.addEventListener('scroll', handleScroll);

    // Clean up event listener on unmount
    return () => {
      element.removeEventListener('scroll', handleScroll);
    };
  }, [ref]);
}