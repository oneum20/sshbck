package websocket

import (
	"context"
	"encoding/base64"
	"log"
	"net/http"
	"sync"

	"sshbck/pkg/sshclient"

	"github.com/gorilla/websocket"
)

// 상수 정의
const (
	ActionConnect         Action = "connection"
	ActionResize          Action = "resize"
	ActionTerminal        Action = "terminal"
	ActionGetFileList     Action = "getfilelist"
	ActionGetFileContents Action = "getfilecontents"
	ActionGetGroups       Action = "getgroups"
	ActionSaveFileChunk   Action = "savefilechunk"
	ActionAddFile         Action = "addfile"
	ActionRemoveFile      Action = "removefile"
)

// 타입 정의
type (
	Action string
	Status string
)

type (
	WSMessage struct {
		Action Action                 `json:"action"`
		Data   map[string]interface{} `json:"data"`
		Status Status                 `json:"status"`
		Error  string                 `json:"error,omitempty"`
	}

	WSError struct {
		Message string `json:"message"`
	}
)

type (
	SafeWebSocket struct {
		Conn  *websocket.Conn
		Mutex sync.Mutex
	}

	WSHandlerContext struct {
		ctx    context.Context
		ssh    *sshclient.SSHContext
		safeWS *SafeWebSocket
		done   chan struct{}
		cancel context.CancelFunc
	}
)

const (
	StatusSuccess    Status = "success"
	StatusFailed     Status = "failed"
	StatusInProgress Status = "in-progress"
)

func newWSHandlerContext(ws *SafeWebSocket) *WSHandlerContext {
	ctx, cancel := context.WithCancel(context.Background())
	return &WSHandlerContext{
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
		ssh:    sshclient.NewSSHContext(),
		safeWS: ws,
	}
}

// 기본 메시지 전송
func (ws *SafeWebSocket) WriteMessage(messageType int, data []byte) error {
	ws.Mutex.Lock()
	defer ws.Mutex.Unlock()
	return ws.Conn.WriteMessage(messageType, []byte(base64.StdEncoding.EncodeToString(data)))
}

// JSON 데이터 전송
func (ws *SafeWebSocket) WriteJSON(data []byte) error {
	ws.Mutex.Lock()
	defer ws.Mutex.Unlock()
	return ws.Conn.WriteJSON(data)
}

// WebSocket을 통해 오류 메시지 전송
func (ws *SafeWebSocket) SendError(msg WSMessage) {
	message := createMessage(string(msg.Action), nil, msg.Status, msg.Error)
	ws.WriteJSON(message)
}

// 메시지 핸들러 맵
var messageHandlers = map[Action]messageHandler{
	ActionConnect:         handleConnect,
	ActionResize:          handleResize,
	ActionTerminal:        handleTerminal,
	ActionGetFileContents: handleGetFileContents,
	ActionSaveFileChunk:   handleSaveFileChunk,
	ActionGetFileList:     handleGetFileList,
	ActionGetGroups:       handleGetGroups,
	ActionAddFile:         handleAddFile,
	ActionRemoveFile:      handleRemoveFile,
}

// 메시지 라우터 설정
func setupMessageRouter() *messageRouter {
	router := NewMessageRouter()

	for action, handler := range messageHandlers {
		router.RegisterHandler(action, handler)
	}

	return router
}

func handleMessages(wsCtx *WSHandlerContext, router *messageRouter) {
	defer func() {
		log.Println("handleMessages done")
		close(wsCtx.done)
		wsCtx.cancel()
	}()

	for {
		var msg WSMessage
		err := wsCtx.safeWS.Conn.ReadJSON(&msg)
		if err != nil {
			log.Println("Read error:", err)
			return
		}

		if err := router.Route(wsCtx, msg); err != nil {
			log.Println("Route error:", err)
			msg.Status = StatusFailed
			msg.Error = err.Error()
			wsCtx.safeWS.SendError(msg)
		}
	}
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

	wsCtx := newWSHandlerContext(&SafeWebSocket{Conn: conn})

	go handleMessages(wsCtx, setupMessageRouter())

	<-wsCtx.done

	log.Println("HandleWebSocket done")
}
