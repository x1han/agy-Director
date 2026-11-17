// Command agy-untun 让 Antigravity 桌面版与 agy CLI 走本地代理通道，
// 全程不启用 TUN。
//
// 设计与使用见 docs/ 下的架构评审报告。
package main

import (
	"fmt"
	"os"
)

// usage 是顶层帮助文本。
const usage = `agy-untun — 让 Antigravity 走本地代理通道（无需 TUN）

用法:
  agy-untun [选项]             等价于 agy-untun launch（双击即用的默认动作）
  agy-untun launch [选项]     探测通道并启动 Antigravity
  agy-untun channels [子命令]  管理代理通道
  agy-untun doctor             诊断安装、通道与运行环境
  agy-untun wrap <命令>...     在代理环境变量下运行任意命令
  agy-untun version            显示版本
  agy-untun help               显示本帮助

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

func main() {
	args := normalizeArgs(os.Args[1:])

	var err error
	switch args[0] {
	case "launch":
		err = runLaunch(args[1:])
	case "channels":
		err = runChannels(args[1:])
	case "doctor":
		err = runDoctor(args[1:])
	case "wrap":
		err = runWrap(args[1:])
	case "version":
		fmt.Println("agy-untun " + version)
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
// **无参数时默认执行 launch。**
//
// 这样双击 agy-untun.exe（或从桌面快捷方式启动）就是
// "一键启动 Antigravity"，而不是打印一段帮助然后闪退。
// 想要帮助信息用 help / -h / --help。
func normalizeArgs(args []string) []string {
	if len(args) == 0 {
		return []string{"launch"}
	}
	return args
}

// version 由构建时 -ldflags 注入。
var version = "dev"
