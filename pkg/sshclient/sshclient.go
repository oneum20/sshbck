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
	Queue  *queue.Queue

	Client     *ssh.Client
	Session    *ssh.Session
	SFTPClient *sftp.Client

	userCache  map[uint32]string // UID -> Username cache
	groupCache map[uint32]string // GID -> Groupname cache
	cacheMutex sync.Mutex        // Mutex for cache concurrency
}

type Config struct {
	ServerConfig *ssh.ClientConfig
	Protocol     string
	Address      string
}

type FileInfo struct {
	Name  string `json:"name"`
	IsDir bool   `json:"isDir"`
	Owner string `json:"owner"`
	Group string `json:"group"`
	Perm  string `json:"perm"`
	Size  int64  `json:"size"`
}

// SSH context 생성
func NewSSHContext() *SSHContext {
	return &SSHContext{
		Queue:      queue.NewQueue(),
		userCache:  make(map[uint32]string),
		groupCache: make(map[uint32]string),
	}
}

// SSH 연결 생성 함수
func (cfg Config) NewConn() (*ssh.Client, error) {
	conn, err := ssh.Dial(cfg.Protocol, cfg.Address, cfg.ServerConfig)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// SSH 세션 생성 함수
func (cfg Config) NewSession(conn *ssh.Client) (*ssh.Session, error) {
	session, err := conn.NewSession()
	if err != nil {
		return nil, err
	}
	return session, nil
}

// Stdout를 지속적으로 읽고 출력 내용을 Queue에 추가
func (ctx *SSHContext) Read() {
	buf := make([]byte, 1024)
	for {
		n, err := ctx.Stdout.Read(buf)
		if err != nil && err != io.EOF {
			log.Println("I/O read error:", err)
			return
		}
		if n > 0 {
			ctx.Queue.Push(buf[:n])
			buf = make([]byte, 1024) // Clear buffer
		}
	}
}

// SSH 명령 실행
func (ctx *SSHContext) ExecuteCommand(cmd string) (string, error) {
	var stdoutBuf bytes.Buffer

	session, err := ctx.Client.NewSession()
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

// UID와 GID를 사용자와 그룹 이름으로 변환하여 캐시에 저장
func (ctx *SSHContext) getOwnerGroupName(uid, gid uint32) (string, string) {
	ctx.cacheMutex.Lock()
	defer ctx.cacheMutex.Unlock()

	// UID가 캐시에 있는지 확인
	ownerName, found := ctx.userCache[uid]
	if !found {
		ownerCmd := fmt.Sprintf("getent passwd %d | cut -d: -f1", uid)
		ownerName, err := ctx.ExecuteCommand(ownerCmd)
		if err != nil {
			log.Printf("Failed to get owner name for UID %d: %v", uid, err)
			ownerName = fmt.Sprintf("%d", uid)
		}
		ownerName = strings.TrimSpace(ownerName)
		ctx.userCache[uid] = ownerName
	}

	// GID가 캐시에 있는지 확인
	groupName, found := ctx.groupCache[gid]
	if !found {
		groupCmd := fmt.Sprintf("getent group %d | cut -d: -f1", gid)
		groupName, err := ctx.ExecuteCommand(groupCmd)
		if err != nil {
			log.Printf("Failed to get group name for GID %d: %v", gid, err)
			groupName = fmt.Sprintf("%d", gid)
		}
		groupName = strings.TrimSpace(groupName)
		ctx.groupCache[gid] = groupName
	}

	return ownerName, groupName
}

// 홈 디렉토리 반환
func (ctx *SSHContext) HomeDir() (string, error) {
	homeDir, err := ctx.ExecuteCommand("pwd")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(homeDir), nil
}

// SFTP를 이용하여 파일 읽기
func (ctx *SSHContext) GetFile(path string) (*sftp.File, error) {
	file, err := ctx.SFTPClient.Open(path)
	if err != nil {
		return nil, err
	}
	return file, nil
}

// 특정 경로의 파일 목록을 반환
func (ctx *SSHContext) GetFileList(root string) ([]FileInfo, error) {
	var filesList []FileInfo
	files, err := ctx.SFTPClient.ReadDir(root)
	if err != nil {
		return nil, err
	}

	for _, file := range files {
		stat := file.Sys().(*sftp.FileStat)
		ownerName, groupName := ctx.getOwnerGroupName(stat.UID, stat.GID)
		perm := file.Mode().Perm().String()

		fileInfo := FileInfo{
			Name:  file.Name(),
			IsDir: file.IsDir(),
			Owner: ownerName,
			Group: groupName,
			Perm:  perm,
			Size:  file.Size(),
		}
		filesList = append(filesList, fileInfo)
	}

	return filesList, nil
}

// 특정 사용자가 속한 그룹 목록 조회
func (ctx *SSHContext) GetGroups() ([]string, error) {
	groupsStr, err := ctx.ExecuteCommand("groups")
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSpace(groupsStr), " "), nil
}

// 해당 파일에 대한 쓰기 권한 확인 (디렉토리 X)
func (ctx *SSHContext) CheckWritePermissionWithStat(path string) bool {
	fileInfo, err := ctx.SFTPClient.Stat(path)
	if err != nil {
		log.Println("Error getting file info:", err)
		return false
	}

	// 파일의 소유자, 그룹, 기타 사용자 권한 검사
	mode := fileInfo.Mode()
	if mode&0200 != 0 { // 소유자 쓰기 권한 확인
		return true
	}
	if mode&0020 != 0 { // 그룹 쓰기 권한 확인
		return true
	}
	if mode&0002 != 0 { // 기타 사용자 쓰기 권한 확인
		return true
	}
	return false
}
