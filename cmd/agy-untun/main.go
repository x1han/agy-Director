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
  agy-untun launch [选项]     探测通道并启动 Antigravity
  agy-untun channels [子命令]  管理代理通道
  agy-untun doctor             诊断安装、通道与运行环境
  agy-untun wrap <命令>...     在代理环境变量下运行任意命令
  agy-untun version            显示版本

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
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "launch":
		err = runLaunch(os.Args[2:])
	case "channels":
		err = runChannels(os.Args[2:])
	case "doctor":
		err = runDoctor(os.Args[2:])
	case "wrap":
		err = runWrap(os.Args[2:])
	case "version":
		fmt.Println("agy-untun " + version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "\n错误: %v\n", err)
		os.Exit(1)
	}
}

// version 由构建时 -ldflags 注入。
var version = "dev"
