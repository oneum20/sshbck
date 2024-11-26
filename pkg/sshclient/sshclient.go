package sshclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
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
func (sshCtx *SSHContext) Read(ctx context.Context) {
	buf := make([]byte, 1024)
	for {
		select {
		case <-ctx.Done():
			log.Println("Read context done")
			return
		default:
			n, err := sshCtx.Stdout.Read(buf)
			if err != nil && err != io.EOF {
				log.Println("I/O read error:", err)
				return
			}
			if n > 0 {
				sshCtx.Queue.Push(buf[:n])
				buf = make([]byte, 1024) // Clear buffer
			}
		}
	}
}

// SSH 명령 실행
func (sshCtx *SSHContext) ExecuteCommand(cmd string) (string, error) {
	var stdoutBuf bytes.Buffer

	session, err := sshCtx.Client.NewSession()
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
func (sshCtx *SSHContext) getOwnerGroupName(uid, gid uint32) (string, string) {
	sshCtx.cacheMutex.Lock()
	defer sshCtx.cacheMutex.Unlock()

	// UID가 캐시에 있는지 확인
	ownerName, found := sshCtx.userCache[uid]
	if !found {
		ownerCmd := fmt.Sprintf("getent passwd %d | cut -d: -f1", uid)
		ownerName, err := sshCtx.ExecuteCommand(ownerCmd)
		if err != nil {
			log.Printf("Failed to get owner name for UID %d: %v", uid, err)
			ownerName = fmt.Sprintf("%d", uid)
		}
		ownerName = strings.TrimSpace(ownerName)
		sshCtx.userCache[uid] = ownerName
	}

	// GID가 캐시에 있는지 확인
	groupName, found := sshCtx.groupCache[gid]
	if !found {
		groupCmd := fmt.Sprintf("getent group %d | cut -d: -f1", gid)
		groupName, err := sshCtx.ExecuteCommand(groupCmd)
		if err != nil {
			log.Printf("Failed to get group name for GID %d: %v", gid, err)
			groupName = fmt.Sprintf("%d", gid)
		}
		groupName = strings.TrimSpace(groupName)
		sshCtx.groupCache[gid] = groupName
	}

	return ownerName, groupName
}

// 홈 디렉토리 반환
func (sshCtx *SSHContext) HomeDir() (string, error) {
	homeDir, err := sshCtx.ExecuteCommand("pwd")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(homeDir), nil
}

// SFTP를 이용하여 파일 읽기
func (sshCtx *SSHContext) GetFile(path string) (*sftp.File, error) {
	file, err := sshCtx.SFTPClient.Open(path)
	if err != nil {
		return nil, err
	}
	return file, nil
}

// 원격 SSH Server에 파일 쓰기
func (sshCtx *SSHContext) SaveFileChunkWithChecksum(path string, content []byte, isFirstChunk bool, isLastChunk bool, checksum string) error {
	tmpPath := "/tmp/" + strings.ReplaceAll(path, "/", "_") + ".tmp"
	var file *sftp.File
	var err error

	if isFirstChunk {
		file, err = sshCtx.SFTPClient.Create(tmpPath)
	} else {
		file, err = sshCtx.SFTPClient.OpenFile(tmpPath, os.O_WRONLY|os.O_APPEND)
	}

	if err != nil {
		return fmt.Errorf("failed to open file: %v", err)
	}
	defer file.Close()

	_, err = file.Write(content)
	if err != nil {
		return fmt.Errorf("faild to write content: %v", err)
	}

	if isLastChunk {
		cmd := fmt.Sprintf("sha256sum %s | awk '{print $1}'", tmpPath)
		tmpFileChecksum, err := sshCtx.ExecuteCommand(cmd)
		if err != nil {
			return fmt.Errorf("failed to execute checksum command : %v", err)
		}

		if strings.TrimSpace(tmpFileChecksum) == strings.TrimSpace(checksum) {
			cmd := fmt.Sprintf("cp -f %s %s", tmpPath, path)
			_, err := sshCtx.ExecuteCommand(cmd)
			if err != nil {
				return fmt.Errorf("failed to overwrite file : %v", err)
			}

			cmd = fmt.Sprintf("rm -f %s", tmpPath)
			_, err = sshCtx.ExecuteCommand(cmd)
			if err != nil {
				return fmt.Errorf("failed to remove file : %v", err)
			}

		} else {
			return fmt.Errorf("failed to write file (missmatch checksum) : %v", err)
		}
	}

	return nil
}

// 특정 경로의 파일 목록을 반환
func (sshCtx *SSHContext) GetFileList(root string) ([]FileInfo, error) {
	var filesList []FileInfo
	files, err := sshCtx.SFTPClient.ReadDir(root)
	if err != nil {
		return nil, err
	}

	for _, file := range files {
		stat := file.Sys().(*sftp.FileStat)
		ownerName, groupName := sshCtx.getOwnerGroupName(stat.UID, stat.GID)
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

// 파일 추가
func (sshCtx *SSHContext) AddFile(path string) error {
	if _, err := sshCtx.ExecuteCommand(fmt.Sprintf("touch %s", path)); err != nil {
		return err
	}

	return nil
}

// 파일 삭제
func (sshCtx *SSHContext) RemoveFile(path string) error {
	if _, err := sshCtx.ExecuteCommand(fmt.Sprintf("rm -f %s", path)); err != nil {
		return err
	}
	return nil
}

// 특정 사용자가 속한 그룹 목록 조회
func (sshCtx *SSHContext) GetGroups() ([]string, error) {
	groupsStr, err := sshCtx.ExecuteCommand("groups")
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSpace(groupsStr), " "), nil
}

// 해당 파일에 대한 쓰기 권한 확인
func (sshCtx *SSHContext) CheckWritePermission(path string) bool {
	file, err := sshCtx.SFTPClient.OpenFile(path, os.O_WRONLY)
	if err != nil {
		// 파일을 열 수 없으면 쓰기 권한이 없음
		return false
	}
	defer file.Close()
	// 파일을 열 수 있으면 쓰기 권한이 있음
	return true
}
