//go:build darwin

package console

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

// noRelaunchEnv 是防止无限重启的环境变量。
//
// 从 .app 双击 → 我们检测到没有终端 → 拉 Terminal.app 重启自己 →
// 新的进程在终端里跑，此时 stdout 是 tty，不会再触发重启。
// 这个变量是第二道保险：万一 tty 检测在某些环境下失灵，
// 也不会变成"拉终端 → 再拉终端"的死循环。
const noRelaunchEnv = "AGY_DIRECTOR_NO_RELAUNCH"

// prepare 在 macOS 上准备终端。
//
// ── 从 .app 双击时会发生什么 ──────────────────────────────────────────
//
// LaunchServices 启动 .app 时，会把 stdout/stderr 接到 **/dev/null**，
// 进程**没有**终端可渲染。所以必须先让 Terminal.app 开一个窗口来跑我们。
//
// 从终端里直接跑二进制（没有 .app bundle）时什么都不用做。
func prepare() (bool, func(), error) {
	noop := func() {}

	// 已经在终端里 → 什么都不用做。
	if term.IsTerminal(int(os.Stdout.Fd())) {
		return true, noop, nil
	}

	// 不在 .app bundle 里 → 也没别的办法（用户就是 `agy-Director > out.txt`），
	// 退化到纯文本输出。
	if !InAppBundle() {
		return false, noop, nil
	}

	// 已经在重启过程中 → 不要再来一次。
	if os.Getenv(noRelaunchEnv) == "1" {
		return false, noop, nil
	}

	if err := RelaunchInTerminal(); err != nil {
		return false, noop, fmt.Errorf("在 Terminal.app 中重新启动失败: %w", err)
	}
	return false, noop, nil
}

// InAppBundle 判断当前进程是否运行在 .app bundle 内。
func InAppBundle() bool {
	return bundleRoot() != ""
}

// bundleRoot 返回 .app 的路径（不含 /.app 后缀），不在 bundle 内返回 ""。
func bundleRoot() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	// os.Executable 返回的是解析后的真实路径，可能带 /private 前缀
	// （/var → /private/var），统一规范一下再判断。
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	i := strings.Index(exe, ".app/Contents/MacOS/")
	if i < 0 {
		return ""
	}
	return exe[:i+len(".app")]
}

// RelaunchInTerminal 用 Terminal.app 新开一个窗口跑当前进程。
//
// ── 为什么要用 argv 传参 ──────────────────────────────────────────────
//
// osascript 的 `do script` 参数是要**丢给 shell 执行**的命令行，
// 于是路径要过两层转义：AppleScript 字符串字面量 + shell。
// 路径里有空格或引号就极易出错。
//
// 用 `on run argv` 让 osascript 自己从 argv 取值，
// 路径的转义就只剩 shell 这一层，我们用 shellQuote 处理好即可。
//
// 用法：
//
//	osascript -e 'on run argv' \
//	          -e 'tell application "Terminal" to activate && do script (item 1 of argv)' \
//	          -e 'end run' -- "<cmd>"
//
// 注意 `--` 之后才是 argv，少了它参数会被 osascript 自己吃掉。
func RelaunchInTerminal() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	parts := []string{noRelaunchEnv + "=1", shellQuote(exe)}
	for _, a := range os.Args[1:] {
		parts = append(parts, shellQuote(a))
	}
	// 命令跑完后 Terminal 的 shell 不会退出（除非我们 exec），
	// 但加上 read 可以让窗口停在"已完成"的状态，用户看得清楚。
	cmd := strings.Join(parts, " ") + "; printf '\\n[agy-Director 已结束，回车关闭] '; read -r _"

	script := `on run argv
	set shellCommand to item 1 of argv
	tell application "Terminal"
		activate
		do script shellCommand
	end tell
end run`

	return exec.Command("osascript", "-e", script, "--", cmd).Run()
}

// shellQuote 做 POSIX shell 的单引号转义。
//
// 单引号内部不能出现单引号，标准做法是先闭引号、转义、再开引号：
//
//	it's  →  'it'\''s'
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
