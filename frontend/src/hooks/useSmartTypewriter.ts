import { useState, useEffect, useRef } from 'react';

interface TypewriterState {
  displayedText: string;
  targetText: string;
  isDeleting: boolean;
  commonPrefixLength: number;
}

export function useSmartTypewriter(targetText: string, isComplete: boolean = false) {
  const [state, setState] = useState<TypewriterState>({
    displayedText: '',
    targetText: '',
    isDeleting: false,
    commonPrefixLength: 0,
  });

  const animationFrameRef = useRef<number | null>(null);
  const lastUpdateTimeRef = useRef(Date.now());

  // Find common prefix between two strings
  const findCommonPrefix = (str1: string, str2: string): number => {
    let i = 0;
    while (i < str1.length && i < str2.length && str1[i] === str2[i]) {
      i++;
    }
    return i;
  };

  useEffect(() => {
    // If text is complete (final), display it immediately
    if (isComplete) {
      setState({
        displayedText: targetText,
        targetText: targetText,
        isDeleting: false,
        commonPrefixLength: targetText.length,
      });
      return;
    }

    // Cancel any ongoing animation
    if (animationFrameRef.current) {
      cancelAnimationFrame(animationFrameRef.current);
      animationFrameRef.current = null;
    }

    // If target text hasn't changed, do nothing
    if (targetText === state.targetText) {
      return;
    }

    // Find common prefix
    const commonPrefixLength = findCommonPrefix(state.displayedText, targetText);

    // Update state with new target
    setState(prev => ({
      ...prev,
      targetText,
      commonPrefixLength,
      isDeleting: prev.displayedText.length > commonPrefixLength,
    }));

  }, [targetText, isComplete, state.targetText, state.displayedText]);

  useEffect(() => {
    const animate = () => {
      setState(prev => {
        const { displayedText, targetText, isDeleting, commonPrefixLength } = prev;

        // If we're done, stop animation
        if (displayedText === targetText) {
          return prev;
        }

        // Calculate speed based on whether we're deleting or typing
        const now = Date.now();
        const timeSinceLastUpdate = now - lastUpdateTimeRef.current;
        const baseSpeed = isDeleting ? 5 : 20; // Faster deletion
        const speed = Math.max(5, Math.min(50, baseSpeed));

        // Only update if enough time has passed
        if (timeSinceLastUpdate < speed) {
          animationFrameRef.current = requestAnimationFrame(animate);
          return prev;
        }

        lastUpdateTimeRef.current = now;

        let newDisplayedText = displayedText;
        let newIsDeleting = isDeleting;

        if (isDeleting) {
          // Delete one character at a time until we reach common prefix
          if (displayedText.length > commonPrefixLength) {
            newDisplayedText = displayedText.slice(0, -1);
          } else {
            // Done deleting, switch to typing mode
            newIsDeleting = false;
          }
        } else {
          // Type one character at a time
          if (displayedText.length < targetText.length) {
            newDisplayedText = targetText.slice(0, displayedText.length + 1);
          }
        }

        return {
          ...prev,
          displayedText: newDisplayedText,
          isDeleting: newIsDeleting,
        };
      });

      animationFrameRef.current = requestAnimationFrame(animate);
    };

    // Start animation
    if (state.displayedText !== state.targetText) {
      animationFrameRef.current = requestAnimationFrame(animate);
    }

    return () => {
      if (animationFrameRef.current) {
        cancelAnimationFrame(animationFrameRef.current);
        animationFrameRef.current = null;
      }
    };
  }, [state]);

  return {
    displayedText: state.displayedText,
    isAnimating: state.displayedText !== state.targetText,
    isDeleting: state.isDeleting,
  };
}