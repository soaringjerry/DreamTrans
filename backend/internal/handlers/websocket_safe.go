package handlers

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// safeWebSocketConn serializes all writes to a Gorilla WebSocket connection.
// Gorilla permits one concurrent reader and one concurrent writer; several of
// our handlers have independent delivery, heartbeat, and billing goroutines.
type safeWebSocketConn struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func newSafeWebSocketConn(conn *websocket.Conn) *safeWebSocketConn {
	return &safeWebSocketConn{conn: conn}
}

func (c *safeWebSocketConn) WriteMessage(messageType int, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
		return err
	}
	return c.conn.WriteMessage(messageType, data)
}

func (c *safeWebSocketConn) WriteJSON(v interface{}) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
		return err
	}
	return c.conn.WriteJSON(v)
}

func (c *safeWebSocketConn) WriteControl(messageType int, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteControl(messageType, data, time.Now().Add(writeWait))
}

func (c *safeWebSocketConn) Close() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.Close()
}
