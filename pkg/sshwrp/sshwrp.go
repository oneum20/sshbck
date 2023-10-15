package sshwrp

import (
	"io"
	"log"

	"golang.org/x/crypto/ssh"
)

type SSHBuf struct {
	Stdin  io.WriteCloser
	Stdout io.Reader
	Data   chan []byte
}

type Config struct {
	ServerConfig *ssh.ClientConfig
	Protocol     string
	Addr         string
}

func (c Config) NewConn() *ssh.Client {
	conn, err := ssh.Dial(c.Protocol, c.Addr, c.ServerConfig)
	if err != nil {
		log.Fatal(err)
	}

	return conn
}

func (c Config) NewSession(conn *ssh.Client) *ssh.Session {
	session, err := conn.NewSession()
	if err != nil {
		log.Fatal(err)
	}

	return session
}

func (s SSHBuf) Read() {
	buf := make([]byte, 1024)
	for {
		n, err := s.Stdout.Read(buf)
		if err != nil {
			log.Fatal(err)
		}

		s.Data <- buf[:n]
	}
}

func (s SSHBuf) Write(input []byte) {
	_, err := s.Stdin.Write(input)
	if err != nil {
		log.Fatal(err)
	}
}
