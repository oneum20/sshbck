package websocket

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

// Json 변환
func toJSON(data map[string]interface{}) ([]byte, error) {
	return json.Marshal(data)
}

// Generates a unique hash for a file path
func generateUniqueHash(filePath string) string {
	hash := sha256.Sum256([]byte(filePath + time.Now().String()))
	return fmt.Sprintf("%x", hash)
}

// WebSocket 메시지 생성 함수
func createMessage(action string, data []byte) []byte {
	message, err := json.Marshal(map[string]interface{}{
		"action": action,
		"data":   data, // Marshal 함수로 인해, []byte가 base64 인코딩 됨
	})
	if err != nil {
		log.Println("JSON marshal error:", err)
		return nil
	}
	return message
}
