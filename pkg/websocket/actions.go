package websocket

import (
	"context"
	"errors"
	"log"
	"sshbck/pkg/sshclient"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// 연결 처리
func handleConnect(wsCtx *WSHandlerContext, requestData map[string]interface{}) error {
	// 클라이언트로 큐의 터미널 메시지 전송
	go func(ctx context.Context) {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				if wsCtx.ssh.Queue.Len() > 0 {
					data := createMessage(string(ActionTerminal), wsCtx.ssh.Queue.Pop().([]byte))
					if err := wsCtx.safeWS.WriteMessage(websocket.TextMessage, data); err != nil {
						log.Println("WebSocket write error:", err)
						return
					}
				} else {
					time.Sleep(100 * time.Millisecond)
				}
			}
		}
	}(wsCtx.ctx)

	// SSH 및 SFTP 연결 설정
	go setupSSHSFTP(wsCtx, requestData)

	return nil
}

// 터미널 리사이즈
func handleResize(wsCtx *WSHandlerContext, requestData map[string]interface{}) error {
	resizePTY(wsCtx.ssh.Session, requestData)
	return nil
}

// 터미널 메시지를 SSH 서버로 전송
func handleTerminal(wsCtx *WSHandlerContext, requestData map[string]interface{}) error {
	termMsg := requestData["data"].(string)
	if wsCtx.ssh.Stdin == nil {
		return errors.New("stdin is nil")
	}
	if _, err := wsCtx.ssh.Stdin.Write([]byte(termMsg)); err != nil {
		return errors.New("write error: " + err.Error())
	}
	return nil
}

func handleGetGroups(wsCtx *WSHandlerContext, requestData map[string]interface{}) error {
	groups, err := wsCtx.ssh.GetGroups()
	if err != nil {
		return errors.New("group retrieval error: " + err.Error())
	}

	msg, err := toJSON(map[string]interface{}{"groups": groups})
	if err != nil {
		return errors.New("json marshal error: " + err.Error())
	}

	wsCtx.safeWS.WriteJSON(createMessage(string(ActionGetGroups), msg))
	return nil
}

// terminal pty 리사이즈
func resizePTY(session *ssh.Session, data map[string]interface{}) {
	cols := int(data["cols"].(float64))
	rows := int(data["rows"].(float64))
	session.WindowChange(rows, cols)
}

// SSH 및 SFTP 연결 설정
func setupSSHSFTP(wsCtx *WSHandlerContext, config map[string]interface{}) error {
	log.Println("setupSSHSFTP", config)
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
		return errors.New("ssh connection error: " + err.Error())
	}
	defer conn.Close()

	session, err := sshConfig.NewSession(conn)
	if err != nil {
		return errors.New("ssh session error: " + err.Error())
	}
	defer session.Close()

	session.RequestPty("xterm", rows, cols, ssh.TerminalModes{})

	wsCtx.ssh.Client = conn
	wsCtx.ssh.Session = session
	wsCtx.ssh.Stdin, _ = session.StdinPipe()
	wsCtx.ssh.Stdout, _ = session.StdoutPipe()

	// Set up SFTP client
	wsCtx.ssh.SFTPClient, err = sftp.NewClient(conn)
	if err != nil {
		return errors.New("sftp client setup error: " + err.Error())
	}
	defer wsCtx.ssh.SFTPClient.Close()

	go wsCtx.ssh.Read(wsCtx.ctx)

	session.Shell()

	<-wsCtx.ctx.Done()
	return nil
}
