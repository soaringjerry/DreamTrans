import { useRef, useCallback, useEffect, useState } from 'react';

// FOR DEBUGGING: Print the environment variable to the browser console
console.log('VITE_BACKEND_WS_URL from env:', import.meta.env.VITE_BACKEND_WS_URL);
console.log('All Vite env vars:', import.meta.env);

type WebSocketStatus = 'connecting' | 'open' | 'closed' | 'error';

interface UseBackendWebSocketReturn {
  connect: () => void;
  sendMessage: (data: any) => void;
  disconnect: () => void;
  status: WebSocketStatus;
}

const BACKEND_WS_URL = import.meta.env.VITE_BACKEND_WS_URL || 'ws://localhost:8080';
console.log('Final BACKEND_WS_URL being used:', BACKEND_WS_URL);

export const useBackendWebSocket = (): UseBackendWebSocketReturn => {
  const wsRef = useRef<WebSocket | null>(null);
  const statusRef = useRef<WebSocketStatus>('closed');
  const [status, setStatus] = useState<WebSocketStatus>('closed');
  
  // Reconnection state management
  const reconnectAttemptsRef = useRef(0);
  const maxReconnectAttempts = 5;
  const reconnectTimeoutRef = useRef<number | null>(null);
  const manuallyDisconnectedRef = useRef(false);

  // Create a ref to hold the connect function
  const connectRef = useRef<(() => void) | null>(null);

  const reconnect = useCallback(() => {
    if (reconnectAttemptsRef.current >= maxReconnectAttempts) {
      console.error('Max reconnect attempts reached. Giving up.');
      statusRef.current = 'error';
      setStatus('error');
      return;
    }
    if (manuallyDisconnectedRef.current) {
      console.log('Manual disconnect, not reconnecting.');
      return;
    }

    reconnectAttemptsRef.current++;
    // Exponential backoff with jitter
    const delay = Math.min(30000, (Math.pow(2, reconnectAttemptsRef.current) * 1000) + (Math.random() * 1000));
    
    console.log(`WebSocket disconnected. Attempting to reconnect in ${Math.round(delay / 1000)}s... (Attempt ${reconnectAttemptsRef.current}/${maxReconnectAttempts})`);

    if (reconnectTimeoutRef.current) {
      clearTimeout(reconnectTimeoutRef.current);
    }

    reconnectTimeoutRef.current = window.setTimeout(() => {
      if (connectRef.current) {
        connectRef.current();
      }
    }, delay);
  }, []);

  const connect = useCallback(() => {
    manuallyDisconnectedRef.current = false; // Reset on new connect attempt
    
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      console.log('WebSocket already connected');
      return;
    }

    try {
      const ws = new WebSocket(`${BACKEND_WS_URL}/ws/translate`);
      
      ws.onopen = () => {
        console.log('WebSocket connected to backend');
        statusRef.current = 'open';
        setStatus('open');
        reconnectAttemptsRef.current = 0; // Reset on successful connection
        if (reconnectTimeoutRef.current) {
          clearTimeout(reconnectTimeoutRef.current);
          reconnectTimeoutRef.current = null;
        }
      };

      ws.onclose = () => {
        console.log('WebSocket disconnected from backend');
        statusRef.current = 'closed';
        setStatus('closed');
        
        // Trigger reconnect logic if not manually disconnected
        if (!manuallyDisconnectedRef.current) {
          reconnect();
        }
      };

      ws.onerror = (error) => {
        console.error('WebSocket error:', error);
        statusRef.current = 'error';
        setStatus('error');
      };

      ws.onmessage = (event) => {
        console.log('Received message from backend:', event.data);
      };

      wsRef.current = ws;
      statusRef.current = 'connecting';
      setStatus('connecting');
    } catch (error) {
      console.error('Failed to create WebSocket:', error);
      statusRef.current = 'error';
      setStatus('error');
    }
  }, [reconnect]);

  // Store the connect function in the ref
  connectRef.current = connect;

  const sendMessage = useCallback((data: any) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      const message = typeof data === 'string' ? data : JSON.stringify(data);
      wsRef.current.send(message);
      console.log('Sent message to backend:', message);
    } else {
      console.warn('WebSocket is not open. Current state:', wsRef.current?.readyState);
    }
  }, []);

  const disconnect = useCallback(() => {
    manuallyDisconnectedRef.current = true; // Set manual disconnect flag
    if (reconnectTimeoutRef.current) {
      clearTimeout(reconnectTimeoutRef.current);
      reconnectTimeoutRef.current = null;
    }
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }
  }, []);

  useEffect(() => {
    return () => {
      disconnect();
    };
  }, [disconnect]);

  return {
    connect,
    sendMessage,
    disconnect,
    status,
  };
};