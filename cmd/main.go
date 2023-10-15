package main

import (
	"fmt"
	"net/http"
	"sshbck/pkg/websocket"
)

func main() {
	http.HandleFunc("/ws", websocket.HandleWebSocket)
	fmt.Println("WebSocket server started on :8080")
	http.ListenAndServe(":8080", nil)
}
