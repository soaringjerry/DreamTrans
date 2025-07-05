package handlers

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// 警告：仅用于开发！生产环境需要更严格的配置
		return true
	},
}

func HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	// 升级 HTTP 连接为 WebSocket 连接
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade connection: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("WebSocket connection established from %s", r.RemoteAddr)

	// 消息读取循环
	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			log.Printf("WebSocket connection closed from %s", r.RemoteAddr)
			break
		}

		// 打印接收到的消息
		log.Printf("Received message (type %d) from %s: %s", messageType, r.RemoteAddr, string(message))

		// TODO: 在这里添加消息处理逻辑（如翻译）
		// 现在只是简单地记录消息
	}
}
