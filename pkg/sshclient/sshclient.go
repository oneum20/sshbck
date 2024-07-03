package sshclient

import (
	"bytes"
	"io"
	"log"

	"sshbck/pkg/queue"

	"golang.org/x/crypto/ssh"
)

type SSHContext struct {
	Stdin  io.WriteCloser
	Stdout io.Reader
	Q      *queue.Queue

	Client  *ssh.Client
	Session *ssh.Session
}

type Config struct {
	ServerConfig *ssh.ClientConfig
	Protocol     string
	Addr         string
}

func (c Config) NewConn() (*ssh.Client, error) {
	conn, err := ssh.Dial(c.Protocol, c.Addr, c.ServerConfig)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func (c Config) NewSession(conn *ssh.Client) (*ssh.Session, error) {
	session, err := conn.NewSession()
	if err != nil {
		return nil, err
	}

	return session, nil
}

func (s SSHContext) Read() {
	buf := make([]byte, 1024)
	for {
		n, err := s.Stdout.Read(buf)
		if err != nil && err != io.EOF {
			log.Println("io read error: ", err)
		}
		if n == 0 {
			continue
		}

		s.Q.Push(buf[:n])

		// 버퍼 비우기
		buf = make([]byte, 1024)
	}
}

func (s SSHContext) ExecuteCommand(cmd string) (string, error) {
	var stdoutBuf bytes.Buffer

	session, err := s.Client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	session.Stdout = &stdoutBuf
	if err := session.Run(cmd); err != nil {
		return "", err
	}

	return stdoutBuf.String(), nil
}
