package module

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// RuntimeConfig describes a self-contained HTTP executable, never a shell command.
// Release is assigned by the host and is absent from distributable ZIP manifests.
type RuntimeConfig struct {
	OS         string   `json:"os"`
	Arch       string   `json:"arch"`
	Executable string   `json:"executable"`
	Args       []string `json:"args,omitempty"`
	Release    string   `json:"release,omitempty"`
}

var releasePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{20,80}$`)

func (r *RuntimeConfig) validate() error {
	if r.OS == "" || r.Arch == "" || !safePackagePath(r.Executable) || len(r.Args) > 32 {
		return errors.New("模块运行声明需要系统、架构和包内程序路径，最多允许 32 个启动参数")
	}
	if r.Release != "" && !releasePattern.MatchString(r.Release) {
		return errors.New("模块程序版本目录无效")
	}
	for _, arg := range r.Args {
		if len(arg) > 4096 || strings.ContainsRune(arg, 0) {
			return errors.New("模块启动参数无效")
		}
	}
	return nil
}

type runtimeRetry struct {
	attempts int
	next     time.Time
}

func (m *Manager) recordRuntimeFailure(id string) {
	retry := m.runtimeRetries[id]
	retry.attempts++
	delay := time.Second * time.Duration(1<<min(retry.attempts, 6))
	retry.next = time.Now().Add(delay)
	m.runtimeRetries[id] = retry
	_ = m.registry.SetStatus(id, StatusError, "模块程序未启动，系统会自动重试；请检查模块包是否适用于当前服务器")
}

func (m *Manager) validateInstalledRuntime(manifest *Manifest) error {
	r := manifest.Runtime
	if r.OS != runtime.GOOS || r.Arch != runtime.GOARCH {
		return fmt.Errorf("模块包适用于 %s/%s，当前服务器为 %s/%s", r.OS, r.Arch, runtime.GOOS, runtime.GOARCH)
	}
	if !releasePattern.MatchString(r.Release) || manifest.Directory == "" {
		return errors.New("本地模块缺少程序文件，请重新导入 ZIP 模块包")
	}
	for _, directory := range []string{manifest.Directory, filepath.Join(manifest.Directory, ".releases")} {
		if info, err := os.Lstat(directory); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("模块安装目录不能是链接或特殊文件")
		}
	}
	root := filepath.Join(manifest.Directory, ".releases", r.Release)
	if err := regularPackageFile(root, r.Executable); err != nil {
		return errors.New("模块程序文件缺失或无效，请重新导入 ZIP 模块包")
	}
	return nil
}

// The host and module share an OS account; this is process management, not a sandbox.
type moduleProcess struct {
	mu                    sync.Mutex
	refs                  int
	done                  chan struct{}
	kill                  func()
	url, token, releaseID string
}

func (p *moduleProcess) alive() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}
func (p *moduleProcess) retain() { p.mu.Lock(); p.refs++; p.mu.Unlock() }
func (p *moduleProcess) release() {
	p.mu.Lock()
	if p.refs <= 0 {
		p.mu.Unlock()
		return
	}
	p.refs--
	last := p.refs == 0
	p.mu.Unlock()
	if last {
		p.kill()
		<-p.done
	}
}

func (m *Manager) acquireProcess(ctx context.Context, manifest *Manifest) (*moduleProcess, error) {
	if old, ok := m.registry.Get(manifest.ID); ok && old.process != nil {
		if old.process.releaseID == manifest.Runtime.Release && old.process.alive() {
			old.process.retain()
			return old.process, nil
		}
		// A crashed parent can leave helpers alive; stop that tree before recovery.
		if !old.process.alive() {
			old.process.kill()
		}
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, errors.New("无法为模块分配本机端口")
	}
	address := listener.Addr().String()
	_ = listener.Close()
	root, err := filepath.Abs(filepath.Join(manifest.Directory, ".releases", manifest.Runtime.Release))
	if err != nil {
		return nil, err
	}
	executable := filepath.Join(root, filepath.FromSlash(manifest.Runtime.Executable))
	if err := os.Chmod(executable, 0700); err != nil {
		return nil, errors.New("无法设置模块程序权限")
	}
	dataDir, err := filepath.Abs(filepath.Join(m.modulesDir, ".data", manifest.ID))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, errors.New("无法创建模块数据目录")
	}
	p := &moduleProcess{refs: 1, done: make(chan struct{}), token: rand.Text(), url: "http://" + address, releaseID: manifest.Runtime.Release}
	cmd := exec.Command(executable, manifest.Runtime.Args...)
	cmd.Dir = root
	// Do not inherit database URLs, master keys, bootstrap passwords or host tokens.
	for _, key := range []string{"SYSTEMROOT", "WINDIR", "TEMP", "TMP", "TMPDIR", "PATH", "LANG", "TZ", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Env = append(cmd.Env, "JMLAB_MODULE_LISTEN_ADDR="+address, "JMLAB_MODULE_TOKEN="+p.token, "JMLAB_MODULE_DATA_DIR="+dataDir)
	// Untrusted stdout/stderr can include credentials. They are deliberately not forwarded.
	kill, err := startModuleProcess(cmd)
	if err != nil {
		return nil, errors.New("模块程序无法启动，请检查系统、架构和运行依赖")
	}
	p.kill = kill
	go func() { _ = cmd.Wait(); close(p.done) }()
	readyCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		connection, dialErr := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(readyCtx, "tcp4", address)
		if dialErr == nil {
			_ = connection.Close()
			return p, nil
		}
		select {
		case <-p.done:
			p.release()
			return nil, errors.New("模块程序在启动期间退出，请检查模块包和运行依赖")
		case <-readyCtx.Done():
			p.release()
			return nil, errors.New("模块启动超时或操作已取消")
		case <-ticker.C:
		}
	}
}
