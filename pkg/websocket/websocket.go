package websocket

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"sshbck/pkg/queue"
	"sshbck/pkg/sshclient"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

func isJson(s []byte) bool {
	var js map[string]interface{}
	return json.Unmarshal([]byte(s), &js) == nil
}

// setup ssh connection
func setupSSH(sbf *sshclient.SSHContext, scf map[string]interface{}, ws *websocket.Conn) *ssh.Session {
	log.Println("connection info : ", scf)
	addr := scf["host"].(string) + ":" + scf["port"].(string)
	cols := int(scf["cols"].(float64))
	rows := int(scf["rows"].(float64))

	serverConfig := &ssh.ClientConfig{
		User: scf["username"].(string),
		Auth: []ssh.AuthMethod{
			ssh.Password(scf["password"].(string)),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	config := sshclient.Config{
		ServerConfig: serverConfig,
		Protocol:     "tcp",
		Addr:         addr,
	}

	conn, err := config.NewConn()
	if err != nil {
		log.Println(err)
		ws.WriteMessage(websocket.TextMessage, []byte(">> "+err.Error()+"\n\r"))
		return nil
	}
	defer conn.Close()

	session, err := config.NewSession(conn)
	if err != nil {
		log.Println(err)
		ws.WriteMessage(websocket.TextMessage, []byte(">> "+err.Error()+"\n\r"))
		return nil
	}
	defer session.Close()

	session.RequestPty("xterm", rows, cols, ssh.TerminalModes{})

	sbf.Client = conn
	sbf.Session = session
	sbf.Stdin, _ = session.StdinPipe()
	sbf.Stdout, _ = session.StdoutPipe()

	go sbf.Read()

	session.Shell()
	session.Wait()

	return session
}

func newSshPty(session *ssh.Session, scf map[string]interface{}) {
	cols := int(scf["cols"].(float64))
	rows := int(scf["rows"].(float64))

	session.RequestPty("xterm", rows, cols, ssh.TerminalModes{})
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// websocket handler
func HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("WebSocket upgrade error:", err)
		return
	}
	defer conn.Close()

	log.Println("WebSocket connection established.")
	conn.WriteMessage(websocket.TextMessage, []byte(">> Websocket connection established.\n\r"))

	sbf := &sshclient.SSHContext{Q: queue.NewQueue()}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if sbf.Q.Len() > 0 {
				err := conn.WriteMessage(websocket.TextMessage, sbf.Q.Pop().([]byte))
				if err != nil {
					log.Println("websocket write error : ", err)
					return
				}
			} else {
				time.Sleep(100 * time.Millisecond) // 루프 내에서 대기 추가
			}
		}

	}()

	for {
		select {
		case <-done:
			return
		default:
			_, msg, err := conn.ReadMessage()
			if err != nil {
				log.Println("Read error:", err)
				return
			}

			if isJson(msg) {
				var data map[string]interface{}
				if err := json.Unmarshal(msg, &data); err != nil {
					log.Println("json parsing error: ", err)
					continue
				}

				action, ok := data["action"].(string)
				if !ok {
					log.Println("action value is not a string : ", action)
					continue
				}

				switch action {
				case "connection":
					go setupSSH(sbf, data, conn)
				case "resize":
					newSshPty(sbf.Session, data)
				default:
					log.Println("not supported action : ", action)
				}
			} else {
				if sbf.Stdin == nil {
					log.Println(">> Stdin is nil : ", msg)
					continue
				}
				if _, err := sbf.Stdin.Write(msg); err != nil {
					log.Println("Write error:", err)
					return
				}
			}
		}

	}

}
