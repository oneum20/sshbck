package sshclient

import (
	"bytes"
	"io"
	"log"
	"strings"

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

type FileInfo struct {
	Name  string     `json:"name"`
	IsDir bool       `json:"isDir"`
	Files []FileInfo `json:"files,omitempty"`
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

func (s SSHContext) HomeDir() (string, error) {
	r, err := s.ExecuteCommand("pwd")
	if err != nil {
		return "", err
	}

	return strings.ReplaceAll(r, "\n", ""), nil
}

func (s SSHContext) GetFiles(root string) ([]FileInfo, error) {
	var fileTree []FileInfo

	r, err := s.ExecuteCommand("ls -Al " + root)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(r, "\n")
	for _, line := range lines {
		fields := strings.Fields(line)

		if len(fields) < 9 {
			continue
		}

		isDir := fields[0][0] == 'd'
		name := fields[8]

		fileInfo := FileInfo{
			Name:  name,
			IsDir: isDir,
		}

		fileTree = append(fileTree, fileInfo)
	}

	return fileTree, nil
}
