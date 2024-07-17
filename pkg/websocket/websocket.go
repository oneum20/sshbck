package websocket

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"sshbck/pkg/queue"
	"sshbck/pkg/sshclient"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

// Action
const (
	ActionConnect  = "connection"
	ActionResize   = "resize"
	ActionTerminal = "terminal"
	ActionGetFiles = "getfiles"
)

func isJson(s []byte) bool {
	var js map[string]interface{}
	return json.Unmarshal([]byte(s), &js) == nil
}

func handleMessage(action string, data []byte) []byte {
	message, err := json.Marshal(map[string]interface{}{
		"action": action,
		"data":   base64.StdEncoding.EncodeToString(data),
	})

	if err != nil {
		log.Println("json marshal error: ", err)
		return nil
	} else {
		return message
	}
}

// setup ssh connection
func setupSSH(sbf *sshclient.SSHContext, scf map[string]interface{}, ws *websocket.Conn) {
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
		ws.WriteMessage(websocket.TextMessage, handleMessage(ActionTerminal, []byte("ERROR : "+err.Error()+"\n\r")))
		return
	}
	defer conn.Close()

	session, err := config.NewSession(conn)
	if err != nil {
		log.Println(err)
		ws.WriteMessage(websocket.TextMessage, handleMessage(ActionTerminal, []byte("ERROR : "+err.Error()+"\n\r")))
		return
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
}

func ptyResize(session *ssh.Session, data map[string]interface{}) {
	cols := int(data["cols"].(float64))
	rows := int(data["rows"].(float64))

	// session.RequestPty("xterm", rows, cols, ssh.TerminalModes{})
	session.WindowChange(rows, cols)
}

func getFiles(sbf *sshclient.SSHContext, root string) []byte {
	fileTree, err := sbf.GetFiles(root)
	if err != nil {
		log.Println("error: ", err)
	}
	data := map[string]interface{}{
		"parent":   root,
		"fileTree": fileTree,
	}
	dataJson, _ := json.MarshalIndent(data, "", "  ")
	return handleMessage(ActionGetFiles, dataJson)
}

func runCmd(sbf *sshclient.SSHContext, data map[string]interface{}) (string, error) {
	command := string(data["cmd"].(string))

	return sbf.ExecuteCommand(command)
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

	sbf := &sshclient.SSHContext{Q: queue.NewQueue()}

	done := make(chan struct{})
	for {
		select {
		case <-done:
			log.Println("Websocket connection close")
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
				case ActionConnect:
					go func() {
						defer close(done)

						for {
							if sbf.Q.Len() > 0 {
								data := handleMessage(ActionTerminal, sbf.Q.Pop().([]byte))
								err := conn.WriteMessage(websocket.TextMessage, data)
								if err != nil {
									log.Println("websocket write error : ", err)
									return
								}
							} else {
								time.Sleep(100 * time.Millisecond) // 루프 내에서 대기 추가
							}
						}

					}()

					go setupSSH(sbf, data, conn)
				case ActionResize:
					ptyResize(sbf.Session, data)
				case ActionTerminal:
					terminalMessage := data["data"].(string)

					if sbf.Stdin == nil {
						log.Println(">> Stdin is nil : ", msg)
						continue
					}
					if _, err := sbf.Stdin.Write([]byte(terminalMessage)); err != nil {
						log.Println("Write error:", err)
						return
					}
				case ActionGetFiles:
					home := data["root"].(string)
					if home == "HOME_DIR" {
						home, _ = sbf.HomeDir()
					}
					message := getFiles(sbf, home)

					err := conn.WriteMessage(websocket.TextMessage, message)
					if err != nil {
						log.Println("websocket write error : ", err)
						return
					}
				default:
					log.Println("not supported action : ", action)
				}
			}
		}

	}

}
