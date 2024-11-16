package websocket

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"time"

	"sshbck/pkg/sshclient"

	"github.com/gorilla/websocket"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// WebSocket 액션 상수 정의
const (
	ActionConnect         = "connection"
	ActionResize          = "resize"
	ActionTerminal        = "terminal"
	ActionGetFileList     = "getfilelist"
	ActionGetFileContents = "getfilecontents"
	ActionGetGroups       = "getgroups"
)

// Json 변환
func toJSON(data map[string]interface{}) []byte {
	jsonData, _ := json.Marshal(data)
	return jsonData
}

// Json 유효성 검사
func isJSON(data []byte) bool {
	var js map[string]interface{}
	return json.Unmarshal(data, &js) == nil
}

// WebSocket 메시지 생성 함수
func createMessage(action string, data []byte) []byte {
	message, err := json.Marshal(map[string]interface{}{
		"action": action,
		"data":   base64.StdEncoding.EncodeToString(data),
	})
	if err != nil {
		log.Println("JSON marshal error:", err)
		return nil
	}
	return message
}

// SSH 및 SFTP 연결 설정
func setupSSHSFTP(ctx *sshclient.SSHContext, config map[string]interface{}, wsConn *websocket.Conn) {
	addr := config["host"].(string) + ":" + config["port"].(string)
	cols := int(config["cols"].(float64))
	rows := int(config["rows"].(float64))

	serverConfig := &ssh.ClientConfig{
		User: config["username"].(string),
		Auth: []ssh.AuthMethod{
			ssh.Password(config["password"].(string)),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	sshConfig := sshclient.Config{
		ServerConfig: serverConfig,
		Protocol:     "tcp",
		Address:      addr,
	}

	conn, err := sshConfig.NewConn()
	if err != nil {
		sendError(wsConn, fmt.Sprintf("SSH connection error: %v", err))
		return
	}
	defer conn.Close()

	session, err := sshConfig.NewSession(conn)
	if err != nil {
		sendError(wsConn, fmt.Sprintf("SSH session error: %v", err))
		return
	}
	defer session.Close()

	session.RequestPty("xterm", rows, cols, ssh.TerminalModes{})

	ctx.Client = conn
	ctx.Session = session
	ctx.Stdin, _ = session.StdinPipe()
	ctx.Stdout, _ = session.StdoutPipe()

	// Set up SFTP client
	ctx.SFTPClient, err = sftp.NewClient(conn)
	if err != nil {
		log.Println("SFTP client setup error:", err)
		wsConn.WriteMessage(websocket.TextMessage, createMessage(ActionTerminal, []byte("ERROR: "+err.Error()+"\n\r")))
		return
	}
	defer ctx.SFTPClient.Close()

	go ctx.Read()
	session.Shell()
	session.Wait()
}

// terminal pty 리사이즈
func resizePTY(session *ssh.Session, data map[string]interface{}) {
	cols := int(data["cols"].(float64))
	rows := int(data["rows"].(float64))
	session.WindowChange(rows, cols)
}

// Generates a unique hash for a file path
func generateUniqueHash(filePath string) string {
	hash := sha256.Sum256([]byte(filePath + time.Now().String()))
	return fmt.Sprintf("%x", hash)
}

// 파일 콘텐츠를 청크 단위로 읽어 전송
func streamFileContent(ctx *sshclient.SSHContext, path string, ws *websocket.Conn) {
	fileHash := generateUniqueHash(path)
	file, err := ctx.SFTPClient.Open(path)
	if err != nil {
		log.Println("File read error:", err)
		return
	}
	defer file.Close()

	writable := ctx.CheckWritePermissionWithStat(path)

	buf := make([]byte, 4096)
	for {
		n, err := file.Read(buf)
		if n > 0 {
			chunkData := map[string]interface{}{
				"fileHash": fileHash,
				"status":   "in-progress",
				"path":     path,
				"writable": writable,
				"content":  base64.StdEncoding.EncodeToString(buf[:n]),
			}
			msg := createMessage(ActionGetFileContents, toJSON(chunkData))
			if err := ws.WriteMessage(websocket.TextMessage, msg); err != nil {
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
	}
	finalChunk := map[string]interface{}{
		"fileHash": fileHash,
		"status":   "complete",
		"path":     path,
		"writable": writable,
		"content":  nil,
	}
	ws.WriteMessage(websocket.TextMessage, createMessage(ActionGetFileContents, toJSON(finalChunk)))
}

// WebSocket을 통해 오류 메시지 전송
func sendError(ws *websocket.Conn, errorMsg string) {
	log.Println(errorMsg)
	message := createMessage(ActionTerminal, []byte("ERROR: "+errorMsg+"\n"))
	ws.WriteMessage(websocket.TextMessage, message)
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// WebSocket handler
func HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("WebSocket upgrade error:", err)
		return
	}
	defer conn.Close()

	ctx := sshclient.NewSSHContext()
	done := make(chan struct{})

	for {
		select {
		case <-done:
			log.Println("WebSocket connection closed")
			return
		default:
			_, msg, err := conn.ReadMessage()
			if err != nil {
				log.Println("Read error:", err)
				return
			}

			if isJSON(msg) {
				var requestData map[string]interface{}
				if err := json.Unmarshal(msg, &requestData); err != nil {
					log.Println("JSON parsing error:", err)
					continue
				}

				action := requestData["action"].(string)
				switch action {
				case ActionConnect:
					go setupSSHSFTP(ctx, requestData, conn)
					go func() {
						defer close(done)
						for {
							if ctx.Queue.Len() > 0 {
								data := createMessage(ActionTerminal, ctx.Queue.Pop().([]byte))
								if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
									log.Println("WebSocket write error:", err)
									return
								}
							} else {
								time.Sleep(100 * time.Millisecond)
							}
						}
					}()
				case ActionTerminal:
					termMsg := requestData["data"].(string)
					if ctx.Stdin == nil {
						log.Println("Stdin is nil")
						continue
					}
					if _, err := ctx.Stdin.Write([]byte(termMsg)); err != nil {
						log.Println("Write error:", err)
						return
					}
				case ActionResize:
					resizePTY(ctx.Session, requestData)
				case ActionGetFileContents:
					go streamFileContent(ctx, requestData["path"].(string), conn)
				case ActionGetFileList:
					root := requestData["root"].(string)
					if root == "HOME_DIR" {
						root, _ = ctx.HomeDir()
					}
					files, err := ctx.GetFileList(root)
					if err != nil {
						log.Println("File list error:", err)
						continue
					}
					data := map[string]interface{}{
						"parent":   path.Clean(root),
						"fileTree": files,
					}
					conn.WriteMessage(websocket.TextMessage, createMessage(ActionGetFileList, toJSON(data)))
				case ActionGetGroups:
					groups, err := ctx.GetGroups()
					if err != nil {
						log.Println("Group retrieval error:", err)
						continue
					}

					conn.WriteMessage(websocket.TextMessage, createMessage(ActionGetGroups, toJSON(map[string]interface{}{"groups": groups})))
				default:
					log.Println("Unsupported action:", action)
				}
			}
		}
	}
}
