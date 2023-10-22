package websocket

import (
	"fmt"
	"log"
	"net/http"

	"sshbck/pkg/sshwrp"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

func setupSSH(sbf *sshwrp.SSHBuf) {
	serverConfig := &ssh.ClientConfig{
		User: "vagrant",
		Auth: []ssh.AuthMethod{
			ssh.Password("vagrant"),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	config := sshwrp.Config{
		ServerConfig: serverConfig,
		Protocol:     "tcp",
		Addr:         "localhost:2222",
	}

	conn := config.NewConn()
	defer conn.Close()

	session := config.NewSession(conn)
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

func HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("WebSocket upgrade error:", err)
		return
	}
	defer conn.Close()

	fmt.Println("WebSocket connection established.")

	sbf := &sshwrp.SSHBuf{Data: make(chan []byte)}

	go setupSSH(sbf)

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
			fmt.Println("Read error:", err)
			return
		}
		sbf.Stdin.Write(msg)
	}

}
