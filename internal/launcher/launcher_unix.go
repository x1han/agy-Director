//go:build !windows

package launcher

import (
	"fmt"
	"io"
	"net/http"
	"syscall"
	"time"
)

// detachAttrs 返回 Unix 上让子进程脱离当前进程组的属性。
// Setsid 让子进程成为新会话首进程，父进程退出不会带走它。
func detachAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// fetchBrowserVersion 请求 CDP 的 /json/version，取出 Antigravity 版本。
//
// 第二个返回值 alive 表示端点是否真的可达——
// DevToolsActivePort 文件可能残留，必须靠这个判断进程是否还活着。
func fetchBrowserVersion(port int, timeout time.Duration) (string, bool) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/json/version", port))
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", false
	}
	return parseVersionFromUserAgent(string(body)), true
}
