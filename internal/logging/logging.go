// Package logging 提供运行期日志：同时写 stderr 和日志文件。
//
// ── 为什么要两个 sink ─────────────────────────────────────────────────
//
//  1. **stderr** 是给人实时看的。跑 `agy-Director doctor` 时，
//     日志直接出现在终端里最顺手。
//
//  2. **文件** 是给人事后查的。关键场景：macOS 从 Finder 双击 .app 时，
//     LaunchServices 把 stdout/stderr 接到 /dev/null —— **stderr 里的东西
//     全部丢失**。Windows 上如果进程没拿到控制台，stderr 同样是死的。
//     所以文件 sink 是硬需求，不是可选项。
//
// ── TUI 期间为什么要静音 stderr ───────────────────────────────────────
//
// Windows 控制台里 stdout 和 stderr 指向**同一块屏幕缓冲区**。
// bubbletea 的 inline 渲染靠"移动光标 + 重写行"定位，
// 中间插进一条 stderr 日志会：
//
//   - 把界面撕裂 / 错位
//   - 被下一次重绘覆盖掉（信息丢失）
//
// 所以进入 TUI 前调 MuteStderr()，退出后调 UnmuteStderr()。
// 文件 sink 全程开着，一行都不丢。
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// state 是 logger 的全部可变状态。
//
// 整个进程只有一个 logger，所以用包级变量 + 一把锁就够了。
type state struct {
	mu sync.Mutex

	// logger 是当前的 slog logger。Setup 之前为 nil。
	logger *slog.Logger

	// file 是日志文件句柄。Setup 之后非 nil。
	file io.Writer

	// stderr 是 stderr 的实时输出目标。
	stderr io.Writer

	// muted 为 true 时 stderr 被摘掉（只写文件）。
	muted bool

	// verbose 为 true 时级别是 debug。
	verbose bool
}

var st = state{stderr: os.Stderr}

// Setup 初始化日志，返回日志文件路径与清理函数。
//
// 日志文件在 os.UserConfigDir()/agy-Director/logs/agy-director.log，
// 按次追加（不覆盖）。
func Setup(verbose bool) (string, func(), error) {
	path, err := LogFile()
	if err != nil {
		return "", func() {}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", func() {}, fmt.Errorf("创建日志目录失败: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", func() {}, fmt.Errorf("打开日志文件失败: %w", err)
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	st.file = f
	st.stderr = os.Stderr
	st.muted = false
	st.verbose = verbose
	st.rebuild()

	return path, func() { _ = f.Close() }, nil
}

// LogFile 返回日志文件路径。
func LogFile() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", fmt.Errorf("找不到用户目录: %w", err)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "agy-Director", "logs", "agy-director.log"), nil
}

// MuteStderr 把 stderr sink 摘掉，只保留文件 sink。
//
// 在 TUI 开始渲染前调用，防止日志把界面搅乱。
func MuteStderr() {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.logger == nil || st.muted {
		return
	}
	st.muted = true
	st.rebuild()
}

// UnmuteStderr 把 stderr sink 接回来。
//
// 在 TUI 退出后调用，让后续的日志重新出现在终端里。
func UnmuteStderr() {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.logger == nil || !st.muted {
		return
	}
	st.muted = false
	st.rebuild()
}

// SetVerbose 切换 debug 级别。
func SetVerbose(v bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.verbose = v
	if st.logger != nil {
		st.rebuild()
	}
}

// rebuild 按当前状态重建 logger。
//
// 调用方必须持有 st.mu。
func (s *state) rebuild() {
	var sink io.Writer
	switch {
	case s.file == nil:
		sink = s.stderr
	case s.muted:
		sink = s.file
	default:
		sink = io.MultiWriter(s.file, s.stderr)
	}

	level := slog.LevelInfo
	if s.verbose {
		level = slog.LevelDebug
	}
	s.logger = slog.New(slog.NewTextHandler(sink, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(s.logger)
}

// Debug 记录一条 debug 级日志。
func Debug(msg string, args ...any) { slog.Debug(msg, args...) }

// Info 记录一条 info 级日志。
func Info(msg string, args ...any) { slog.Info(msg, args...) }

// Warn 记录一条 warn 级日志。
func Warn(msg string, args ...any) { slog.Warn(msg, args...) }

// Error 记录一条 error 级日志。
func Error(msg string, args ...any) { slog.Error(msg, args...) }
