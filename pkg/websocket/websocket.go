package websocket

import (
	"encoding/json"
	"log"
	"net/http"

	"sshbck/pkg/sshwrp"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

// setup ssh connection
func setupSSH(sbf *sshwrp.SSHBuf, scf map[string]interface{}, ws *websocket.Conn) {
	addr := scf["host"].(string) + ":" + scf["port"].(string)

	serverConfig := &ssh.ClientConfig{
		User: scf["username"].(string),
		Auth: []ssh.AuthMethod{
			ssh.Password(scf["password"].(string)),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	config := sshwrp.Config{
		ServerConfig: serverConfig,
		Protocol:     "tcp",
		Addr:         addr,
	}

	conn, err := config.NewConn()
	if err != nil {
		log.Println(err)
		ws.WriteMessage(websocket.TextMessage, []byte(">> "+err.Error()+"\n\r"))
		return
	}
	defer conn.Close()

	session, err := config.NewSession(conn)
	if err != nil {
		log.Println(err)
		ws.WriteMessage(websocket.TextMessage, []byte(">> "+err.Error()+"\n\r"))
		return
	}
	defer session.Close()

	session.RequestPty("xterm", 80, 50, ssh.TerminalModes{})

	sbf.Stdin, _ = session.StdinPipe()
	sbf.Stdout, _ = session.StdoutPipe()

	go sbf.Read()

	session.Shell()
	session.Wait()
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
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

	// get remote ssh server info
	_, msg, err := conn.ReadMessage()
	if err != nil {
		log.Println("Read error:", err)
		return
	}

	var sshInfo map[string]interface{}
	if err := json.Unmarshal(msg, &sshInfo); err != nil {
		log.Println("ssh connection error :", err)
		conn.WriteMessage(websocket.TextMessage, []byte(">> ssh connection error : "+err.Error()))
		return
	}

	sbf := &sshwrp.SSHBuf{Data: make(chan []byte)}

	go setupSSH(sbf, sshInfo, conn)

	go func() {
		for {
			if data, ok := <-sbf.Data; len(data) > 0 {
				if !ok {
					return
				}
				err := conn.WriteMessage(websocket.TextMessage, data)
				if err != nil {
					log.Println("websocket write error : ", err)
					return
				}
			}
		}

	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Println("Read error:", err)
			return
		}
		if _, err := sbf.Stdin.Write(msg); err != nil {
			log.Println("Write error:", err)
			return
		}
	}

}
