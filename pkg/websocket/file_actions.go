package websocket

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"path"

	"github.com/gorilla/websocket"
)

type FileChunk struct {
	FileHash string `json:"fileHash"`
	Status   Status `json:"status"`
	Path     string `json:"path"`
	Writable bool   `json:"writable"`
	Index    int    `json:"index"`
	Content  string `json:"content"`
	Checksum string `json:"checksum"`
}

// 파일 목록 조회
func handleGetFileList(wsCtx *WSHandlerContext, requestData map[string]interface{}) error {
	root := requestData["root"].(string)
	if root == "HOME_DIR" {
		root, _ = wsCtx.ssh.HomeDir()
	}

	files, err := wsCtx.ssh.GetFileList(root)
	if err != nil {
		return errors.New("file list error: " + err.Error())
	}

	data := map[string]interface{}{
		"parent":   path.Clean(root),
		"fileTree": files,
	}

	msg, err := toJSON(data)
	if err != nil {
		return errors.New("json marshal error: " + err.Error())
	}

	wsCtx.safeWS.WriteJSON(createMessage(string(ActionGetFileList), msg))
	return nil
}

// 파일 콘텐츠 조회
func handleGetFileContents(wsCtx *WSHandlerContext, requestData map[string]interface{}) error {
	path := requestData["path"].(string)
	go streamFileContent(wsCtx, path)
	return nil
}

// 파일 콘텐츠 저장
func handleSaveFileChunk(wsCtx *WSHandlerContext, requestData map[string]interface{}) error {
	path := requestData["path"].(string)
	content, _ := base64.StdEncoding.DecodeString(requestData["content"].(string))
	isFirstChunk := requestData["isFirstChunk"].(bool)
	isLastChunk := requestData["isLastChunk"].(bool)
	checksum, _ := requestData["checksum"].(string)

	err := wsCtx.ssh.SaveFileChunkWithChecksum(path, content, isFirstChunk, isLastChunk, checksum)
	if err != nil {
		return errors.New("file write error: " + err.Error())
	}

	data := map[string]interface{}{
		"path":   path,
		"status": StatusSuccess,
	}

	msg, err := toJSON(data)
	if err != nil {
		return errors.New("json marshal error: " + err.Error())
	}

	wsCtx.safeWS.WriteJSON(createMessage(string(ActionSaveFileChunk), msg))
	return nil
}

// 파일 콘텐츠 스트리밍
func streamFileContent(wsCtx *WSHandlerContext, path string) {
	fileHash := generateUniqueHash(path)
	file, err := wsCtx.ssh.SFTPClient.Open(path)
	if err != nil {
		log.Println("File read error:", err)
		return
	}
	defer file.Close()

	writable := wsCtx.ssh.CheckWritePermission(path)

	// SHA-256 체크섬 계산기 초기화
	hash := sha256.New()

	buf := make([]byte, 4096)
	idx := 0
	for {
		n, err := file.Read(buf)
		if n > 0 {
			// 데이터 청크를 해시 계산에 추가
			if _, hashErr := hash.Write(buf[:n]); hashErr != nil {
				log.Println("Hash calculation error:", hashErr)
				sendError(wsCtx.safeWS, "Hash calculation error")
				return
			}
			chunkData := FileChunk{
				FileHash: fileHash,
				Status:   StatusInProgress,
				Path:     path,
				Writable: writable,
				Checksum: "",
				Index:    idx,
				Content:  base64.StdEncoding.EncodeToString(buf[:n]),
			}
			msg, err := json.Marshal(chunkData)
			if err != nil {
				log.Println("JSON marshal error:", err)
				return
			}

			if err := wsCtx.safeWS.WriteMessage(websocket.TextMessage, createMessage(string(ActionGetFileContents), msg)); err != nil {
				log.Println("WebSocket write error:", err)
				return
			}
		}
		if err == io.EOF {
			break
		} else if err != nil {
			log.Println("File read error:", err)
			return
		}

		idx++
	}
	finalHash := hash.Sum(nil)

	finalChunk := FileChunk{
		FileHash: fileHash,
		Status:   StatusSuccess,
		Path:     path,
		Writable: writable,
		Index:    idx,
		Content:  "",
		Checksum: fmt.Sprintf("%x", finalHash),
	}
	msg, err := json.Marshal(finalChunk)
	if err != nil {
		log.Println("JSON marshal error:", err)
		return
	}
	err = wsCtx.safeWS.WriteMessage(websocket.TextMessage, createMessage(string(ActionGetFileContents), msg))

	if err != nil {
		log.Println("websocket write error:", err)
	}
}
