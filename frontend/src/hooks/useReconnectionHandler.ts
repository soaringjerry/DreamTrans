import { useState, useEffect, useRef, useCallback } from 'react';

// 定义 Hook 返回值的类型
interface ReconnectionState {
  isReconnecting: boolean;
  attempt: number;
  error: string | null;
}

// 定义 Hook 的参数类型
interface ReconnectionHookProps {
  shouldReconnect: boolean;
  reconnectAction: () => Promise<void>;
  maxRetries?: number;
  initialDelay?: number;
  maxDelay?: number;
}

export function useReconnectionHandler({
  shouldReconnect,
  reconnectAction,
  maxRetries = 10,
  initialDelay = 1000, // 1 second
  maxDelay = 30000,    // 30 seconds
}: ReconnectionHookProps): ReconnectionState {
  const [isReconnecting, setIsReconnecting] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const timeoutRef = useRef<number | null>(null);

  const reset = useCallback(() => {
    setIsReconnecting(false);
    setAttempt(0);
    setError(null);
    if (timeoutRef.current) {
      clearTimeout(timeoutRef.current);
      timeoutRef.current = null;
    }
  }, []);

  useEffect(() => {
    if (!shouldReconnect) {
      reset();
      return;
    }

    if (attempt >= maxRetries) {
      setError(`Failed to reconnect after ${maxRetries} attempts.`);
      setIsReconnecting(false);
      return;
    }

    if (attempt > 0) {
      // Exponential backoff
      const delay = Math.min(initialDelay * Math.pow(2, attempt - 1), maxDelay);
      
      console.log(`Reconnection attempt ${attempt}. Retrying in ${delay}ms...`);

      timeoutRef.current = window.setTimeout(() => {
        reconnectAction()
          .then(() => {
            console.log('Reconnection successful.');
            reset();
          })
          .catch((err) => {
            console.error(`Reconnection attempt ${attempt} failed:`, err);
            setAttempt(prev => prev + 1);
          });
      }, delay);
    } else if (!isReconnecting) {
        // First attempt, immediately
        setIsReconnecting(true);
        setAttempt(1);
    }


    return () => {
      if (timeoutRef.current) {
        clearTimeout(timeoutRef.current);
      }
    };
  }, [shouldReconnect, attempt, maxRetries, initialDelay, maxDelay, reconnectAction, reset, isReconnecting]);

  // This effect handles the very first reconnection attempt
  useEffect(() => {
    if (isReconnecting && attempt === 1) {
        reconnectAction()
            .then(() => {
                console.log('Reconnection successful on first attempt.');
                reset();
            })
            .catch((err) => {
                console.error('Reconnection attempt 1 failed:', err);
                setAttempt(prev => prev + 1);
                // The other useEffect will handle retries
            });
    }
  }, [isReconnecting, attempt, reconnectAction, reset]);


  return { isReconnecting, attempt, error };
}