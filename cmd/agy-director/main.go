// Command agy-Director 让 Antigravity 桌面版与 agy CLI 走本地代理通道，
// 全程不启用 TUN。
//
// 设计与使用见 docs/ 下的架构评审报告。
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// usage 是顶层帮助文本。
const usage = `agy-Director — 让 Antigravity 走本地代理通道（无需 TUN）

用法:
  agy-Director                终端图形界面（TUI）：扫描 → 确认 → 启动
  agy-Director tui [选项]     同上，可加 --verbose / --no-wait
  agy-Director launch [选项]  命令行方式：探测通道并启动 Antigravity
  agy-Director channels [子命令]  管理代理通道
  agy-Director doctor         诊断安装、通道与运行环境
  agy-Director wrap <命令>...  在代理环境变量下运行任意命令
  agy-Director version        显示版本
  agy-Director help           显示本帮助

tui 选项:
  --verbose      输出 debug 级日志
  --no-wait      退出前不等回车（脚本里用）
  --wait=N       摘要页最多等 N 秒（默认 3，期间按回车可立刻关）
  --wait         摘要页一直等到按回车

launch 选项:
  --ide            强制启动桌面版 Antigravity
  --cli            强制启动 agy CLI
  --channel <名>   指定通道（默认用配置里的首选，或自动探测）
  --proxy <URL>    直接指定代理 URL，如 http://127.0.0.1:10808
  --scan           忽略配置，扫描本机寻找可用通道
  --yes            不交互，全部用自动选择
  --               之后的参数原样传给被启动的程序

channels 子命令:
  list             列出已配置的通道
  scan             扫描本机探测可用通道
  add <名> <URL>   添加通道，如 add v2rayN http://127.0.0.1:10808
  remove <名>      移除通道
  test <名|URL>    测试通道能否连通 Antigravity 的后端端点

doctor 选项:
  --processes      只列出「会被终止的进程」，不做任何终止动作
`

// verboseMode 开了 debug 级日志。
var verboseMode bool

// noWait 退出前不等回车。
var noWait bool

// waitTimeout 是摘要页的等待上限（秒）。
//
// 摘要页（目标/通道/代理/日志 那几行）默认最多等 3 秒 ——
// 用户双击图标只想看一眼结果，不想被一个"按回车关闭"卡住。
// 3 秒到了自动关；期间按回车可以立刻关。
//
// 0 表示不自动关（一直等到回车），--no-wait 则完全不等。
var waitTimeout = 3

func main() {
	args := normalizeArgs(os.Args[1:])

	var err error
	switch args[0] {
	case "tui":
		err = runTUI(args[1:])
	case "launch":
		err = runLaunch(args[1:])
	case "channels":
		err = runChannels(args[1:])
	case "doctor":
		err = runDoctor(args[1:])
	case "wrap":
		err = runWrap(args[1:])
	case "version":
		fmt.Println("agy-Director " + version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n%s", args[0], usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "\n错误: %v\n", err)
		os.Exit(1)
	}
}

// normalizeArgs 规范化命令行参数。
//
// **无参数时默认执行 tui。**
//
// 这样双击 agy-Director.exe（或从桌面快捷方式启动）就是
// "打开终端 → 出 TUI 界面 → 确认 → 启动 Antigravity"，
// 而不是打印一段帮助然后闪退。
// 想要纯命令行方式用 launch 子命令。
func normalizeArgs(args []string) []string {
	if len(args) == 0 {
		return []string{"tui"}
	}
	return args
}

// parseTUIArgs 解析 tui 子命令的参数。
func parseTUIArgs(args []string) error {
	for _, a := range args {
		switch {
		case a == "--verbose" || a == "-v":
			verboseMode = true
		case a == "--no-wait":
			noWait = true
		case a == "--wait" || a == "--wait-forever":
			// 摘要页一直等到用户按回车（关掉自动关闭）。
			waitTimeout = 0
		case strings.HasPrefix(a, "--wait="):
			// --wait=10 → 最多等 10 秒。
			secs, err := strconv.Atoi(strings.TrimPrefix(a, "--wait="))
			if err != nil || secs < 0 {
				return fmt.Errorf("无效的等待秒数: %s", strings.TrimPrefix(a, "--wait="))
			}
			waitTimeout = secs
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("未知选项: %s", a)
			}
			return fmt.Errorf("无法识别的参数: %s", a)
		}
	}
	return nil
}

// version 由构建时 -ldflags 注入。
var version = "dev"
