import { useRef, useCallback, useEffect, useState } from 'react';
import { ensureValidAccessToken, getAccessToken } from '../pro/api/auth';

// Environment variables are now properly configured

type WebSocketStatus = 'connecting' | 'open' | 'closed' | 'error';

interface UseBackendWebSocketReturn {
  connect: () => void;
  sendMessage: (data: unknown) => void;
  disconnect: () => void;
  status: WebSocketStatus;
}

const BACKEND_WS_URL = import.meta.env.VITE_BACKEND_WS_URL || 'ws://localhost:8080';
const isProduction = BACKEND_WS_URL === '/';

export const useBackendWebSocket = (onMessage?: (data: unknown) => void): UseBackendWebSocketReturn => {
  const wsRef = useRef<WebSocket | null>(null);
  const statusRef = useRef<WebSocketStatus>('closed');
  const [status, setStatus] = useState<WebSocketStatus>('closed');
  const lastMessageAtRef = useRef<number>(0);
  
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

  const connect = useCallback(async () => {
    manuallyDisconnectedRef.current = false; // Reset on new connect attempt

    if (wsRef.current?.readyState === WebSocket.OPEN || wsRef.current?.readyState === WebSocket.CONNECTING) {
      console.log('WebSocket already connected or connecting');
      return;
    }

    statusRef.current = 'connecting';
    setStatus('connecting');

    try {
      // In production, use relative WebSocket URL
      const baseUrl = isProduction
        ? `${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}/ws/translate`
        : `${BACKEND_WS_URL}/ws/translate`;

      // Send JWT in the WebSocket protocol header so reverse-proxy access
      // logs do not retain credentials in the request URL.
      const url = new URL(baseUrl, window.location.origin);
      const token = getAccessToken() ? await ensureValidAccessToken(90) : null;
      const ws = token
        ? new WebSocket(url.toString(), [`dreamtrans.jwt.${token}`])
        : new WebSocket(url.toString());
      
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
        if (wsRef.current !== ws) return;
        wsRef.current = null;
        console.log('WebSocket disconnected from backend');
        statusRef.current = 'closed';
        setStatus('closed');
        
        // Trigger reconnect logic if not manually disconnected
        if (!manuallyDisconnectedRef.current) {
          reconnect();
        }
      };

      ws.onerror = (error) => {
        if (wsRef.current !== ws) return;
        console.error('WebSocket error:', error);
        statusRef.current = 'error';
        setStatus('error');
      };

      ws.onmessage = (event) => {
        if (wsRef.current !== ws) return;
        lastMessageAtRef.current = Date.now();
        let parsed: unknown = event.data;
        try {
          parsed = JSON.parse(event.data);
        } catch {
          // leave as raw string if not JSON
        }
        if (onMessage) onMessage(parsed);
        else console.log('Received message from backend:', event.data);
      };

      wsRef.current = ws;
      lastMessageAtRef.current = Date.now();
    } catch (error) {
      console.error('Failed to create WebSocket:', error);
      statusRef.current = 'error';
      setStatus('error');
    }
  }, [reconnect, onMessage]);

  // Keep timer callbacks pointed at the latest connect implementation without
  // mutating refs during render.
  useEffect(() => {
    connectRef.current = connect;
    return () => {
      if (connectRef.current === connect) connectRef.current = null;
    };
  }, [connect]);

  const sendMessage = useCallback((data: unknown) => {
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
    statusRef.current = 'closed';
    setStatus('closed');
  }, []);

  useEffect(() => {
    // Heartbeat/ping to keep the WS alive (esp. when tab in background)
    const heartbeatId = window.setInterval(() => {
      if (wsRef.current?.readyState === WebSocket.OPEN) {
        try {
          wsRef.current.send(JSON.stringify({ type: 'ping', ts: Date.now() }));
        } catch (err) {
          console.warn('Heartbeat send failed, forcing reconnect', err);
          try { wsRef.current.close(); } catch { /* noop */ }
        }
      }
    }, 15000);

    // Watchdog: if no messages for a while, force reconnect
    const watchdogId = window.setInterval(() => {
      if (statusRef.current !== 'open') return;
      const now = Date.now();
      if (lastMessageAtRef.current > 0 && (now - lastMessageAtRef.current) > 45000) {
        console.warn('Backend WS idle >45s, forcing reconnect');
        try { wsRef.current?.close(); } catch { /* noop */ }
      }
    }, 10000);

    return () => {
      window.clearInterval(heartbeatId);
      window.clearInterval(watchdogId);
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
