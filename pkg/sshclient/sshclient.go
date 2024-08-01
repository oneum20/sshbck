package sshclient

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"

	"sshbck/pkg/queue"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type SSHContext struct {
	Stdin  io.WriteCloser
	Stdout io.Reader
	Q      *queue.Queue

	Client  *ssh.Client
	Session *ssh.Session
	SFTP    *sftp.Client

	userCache  map[uint32]string // UID -> Username 캐시
	groupCache map[uint32]string // GID -> Groupname 캐시
	cacheMutex sync.Mutex        // 캐시 접근 동시성을 위한 Mutex
}

type Config struct {
	ServerConfig *ssh.ClientConfig
	Protocol     string
	Addr         string
}

type FileInfo struct {
	Name  string `json:"name"`
	IsDir bool   `json:"isDir"`
	Owner string `json:"owner"`
	Group string `json:"group"`
	Perm  string `json:"perm"`
	Size  int64  `json:"size"`
}

func NewSSHContext() *SSHContext {
	return &SSHContext{
		Q:          queue.NewQueue(),
		userCache:  make(map[uint32]string),
		groupCache: make(map[uint32]string),
	}
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

// UID와 GID를 사용자와 그룹 이름으로 변환하고 캐시에 저장
func (c *SSHContext) getOwnerGroupName(uid uint32, gid uint32) (string, string) {
	c.cacheMutex.Lock()
	defer c.cacheMutex.Unlock()

	// UID가 캐시에 있는지 확인
	ownerName, found := c.userCache[uid]
	if !found {
		ownerCmd := fmt.Sprintf("getent passwd %d | cut -d: -f1", uid)
		ownerName, err := c.ExecuteCommand(ownerCmd)
		if err != nil {
			log.Printf("Failed to get owner name for UID %d: %v", uid, err)
			ownerName = fmt.Sprintf("%d", uid)
		}
		ownerName = strings.TrimSpace(ownerName)
		c.userCache[uid] = ownerName // 캐시에 저장
	}

	// GID가 캐시에 있는지 확인
	groupName, found := c.groupCache[gid]
	if !found {
		groupCmd := fmt.Sprintf("getent group %d | cut -d: -f1", gid)
		groupName, err := c.ExecuteCommand(groupCmd)
		if err != nil {
			log.Printf("Failed to get group name for GID %d: %v", gid, err)
			groupName = fmt.Sprintf("%d", gid)
		}
		groupName = strings.TrimSpace(groupName)
		c.groupCache[gid] = groupName // 캐시에 저장
	}

	return ownerName, groupName
}

func (c *SSHContext) Read() {
	buf := make([]byte, 1024)
	for {
		n, err := c.Stdout.Read(buf)
		if err != nil && err != io.EOF {
			log.Println("io read error: ", err)
		}
		if n == 0 {
			continue
		}

		c.Q.Push(buf[:n])

		// 버퍼 비우기
		buf = make([]byte, 1024)
	}
}

func (c *SSHContext) ExecuteCommand(cmd string) (string, error) {
	var stdoutBuf bytes.Buffer

	session, err := c.Client.NewSession()
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

func (c *SSHContext) HomeDir() (string, error) {
	r, err := c.ExecuteCommand("pwd")
	if err != nil {
		return "", err
	}

	return strings.ReplaceAll(r, "\n", ""), nil
}

func (c *SSHContext) GetFiles(root string) ([]FileInfo, error) {
	var fileTree []FileInfo

	files, err := c.SFTP.ReadDir(root)
	if err != nil {
		return nil, err
	}

	for _, file := range files {
		stat := file.Sys().(*sftp.FileStat)
		ownerName, groupName := c.getOwnerGroupName(stat.UID, stat.GID)
		perm := file.Mode().Perm().String()

		fileInfo := FileInfo{
			Name:  file.Name(),
			IsDir: file.IsDir(),
			Owner: ownerName,
			Group: groupName,
			Perm:  perm,
			Size:  file.Size(),
		}

		fileTree = append(fileTree, fileInfo)
	}

	return fileTree, nil
}

func (c *SSHContext) GetGroups() ([]string, error) {
	r, err := c.ExecuteCommand("groups")
	if err != nil {
		return nil, err
	}

	groups := strings.Split(r, " ")

	return groups, nil
}
