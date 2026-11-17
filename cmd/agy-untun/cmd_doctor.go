package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/user/agy-untun/internal/channel"
	"github.com/user/agy-untun/internal/discovery"
	"github.com/user/agy-untun/internal/launcher"
)

// runDoctor 输出完整的诊断报告。
func runDoctor(args []string) error {
	fmt.Println("agy-untun 诊断")
	fmt.Println(strings.Repeat("=", 60))

	section("运行环境")
	fmt.Printf("  平台        %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("  Go 版本     %s\n", runtime.Version())

	section("Antigravity 桌面版")
	ide, ideOK := discovery.FindTarget(discovery.TargetIDE)
	if ideOK {
		fmt.Printf("  ✓ 已找到    %s\n", ide.Path)
		if ide.Version != "" {
			fmt.Printf("    版本      %s\n", ide.Version)
		}
	} else {
		fmt.Println("  ✗ 未找到")
		for _, c := range discovery.IDECandidates() {
			fmt.Printf("      试过 %s\n", c)
		}
	}

	section("agy CLI")
	cli, cliOK := discovery.FindTarget(discovery.TargetCLI)
	if cliOK {
		fmt.Printf("  ✓ 已找到    %s\n", cli.Path)
		if cli.Version != "" {
			fmt.Printf("    版本      %s\n", cli.Version)
		}
	} else {
		fmt.Println("  ✗ 未找到")
		for _, c := range discovery.CLICandidates() {
			fmt.Printf("      试过 %s\n", c)
		}
	}

	section("运行中的实例")
	if inst, running := launcher.CheckRunning(2 * time.Second); running {
		fmt.Printf("  ⚠ 桌面版正在运行（端口 %d%s）\n", inst.Port, versionSuffix(inst.Version))
		fmt.Println("    已有实例不会继承新的代理环境变量，")
		fmt.Println("    启动前必须先退出它。")
	} else {
		fmt.Println("  ✓ 没有运行中的桌面版实例")
	}
	if cliRunning() {
		fmt.Println("  ⚠ agy CLI 正在运行")
	} else {
		fmt.Println("  ✓ 没有运行中的 agy CLI")
	}

	section("代理环境变量")
	printEnvProxy("HTTP_PROXY", os.Getenv("HTTP_PROXY"))
	printEnvProxy("HTTPS_PROXY", os.Getenv("HTTPS_PROXY"))
	printEnvProxy("ALL_PROXY", os.Getenv("ALL_PROXY"))
	printEnvProxy("NO_PROXY", os.Getenv("NO_PROXY"))

	section("Shell 环境冲突（仅 macOS/Linux 有效）")
	if runtime.GOOS == "windows" {
		fmt.Println("  · Windows 上 Antigravity 不读登录 shell 的环境变量，无此冲突")
	} else {
		conflicts := readShellProxyConflicts()
		if len(conflicts) == 0 {
			fmt.Println("  ✓ 未发现冲突")
		} else {
			for _, c := range conflicts {
				fmt.Printf("  ⚠ %s\n", c)
			}
			fmt.Println("    Antigravity 会用登录 shell 的值覆盖启动器注入的值，")
			fmt.Println("    导致通道失效。建议从 rc 文件里移除这些 export。")
		}
	}

	section("本机代理端点")
	candidates, err := discoverChannels()
	if err != nil {
		fmt.Printf("  扫描失败: %v\n", err)
	} else if len(candidates) == 0 {
		fmt.Println("  未发现 http/socks5 端点")
	} else {
		for _, c := range candidates {
			fmt.Printf("  ✓ %-22s %s", c.Addr(), schemeList(c))
			if c.LAN {
				fmt.Printf("  [局域网监听]")
			}
			fmt.Println()
		}
	}

	section("配置的通道")
	cfg, err := loadConfig()
	if err != nil {
		fmt.Printf("  读取失败: %v\n", err)
	} else if len(cfg.Channels) == 0 {
		path, _ := configFile()
		fmt.Printf("  还没有配置任何通道（%s）\n", path)
	} else {
		for _, c := range cfg.Channels {
			mark := " "
			if c.Name == cfg.Preferred {
				mark = "*"
			}
			ok, detail := channel.VerifyChannel(c, 8*time.Second)
			status := "✗"
			if ok {
				status = "✓"
			}
			fmt.Printf(" %s %-16s %-22s %s\n", mark, c.Name, c.Addr(), status)
			if !ok {
				fmt.Printf("     %s\n", detail)
			}
		}
	}

	fmt.Println()
	return nil
}

// section 打印一个分节标题。
func section(title string) {
	fmt.Printf("\n%s\n", title)
}

// printEnvProxy 打印一个代理环境变量。
func printEnvProxy(name, value string) {
	if value == "" {
		fmt.Printf("  %-12s （未设置）\n", name)
		return
	}
	fmt.Printf("  %-12s %s\n", name, value)
}

// cliRunning 检查是否有 agy 进程在跑。
func cliRunning() bool {
	if runtime.GOOS == "windows" {
		out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq agy.exe").Output()
		return err == nil && strings.Contains(string(out), "agy.exe")
	}
	out, err := exec.Command("pgrep", "-x", "agy").Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// readShellProxyConflicts 读取登录 shell 的环境，找出与通道冲突的代理变量。
//
// 背景：Antigravity 的 dist/loginShellEnv.js 在 win32 直接返回空对象，
// 但在 macOS/Linux 会读登录 shell 的环境；
// 而 dist/languageServer.js 里 `const env = { ...process.env, ...loginShellEnv }`
// 让 shell 的值覆盖启动器注入的值。
func readShellProxyConflicts() []string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-ilc", "env")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	watch := []string{"http_proxy", "https_proxy", "all_proxy", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"}
	found := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		idx := strings.Index(line, "=")
		if idx <= 0 {
			continue
		}
		k, v := line[:idx], line[idx+1:]
		for _, w := range watch {
			if k == w && v != "" {
				found[k] = v
			}
		}
	}
	if len(found) == 0 {
		return nil
	}
	var out2 []string
	for _, w := range watch {
		if v, ok := found[w]; ok {
			out2 = append(out2, fmt.Sprintf("%s=%s", w, v))
		}
	}
	return out2
}

// runWrap 在代理环境变量下运行任意命令。
// 这给「让 agy 命令永远走代理」提供了基础：
// 可以生成 shell alias `agy='agy-untun wrap agy'`。
func runWrap(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: agy-untun wrap <命令> [参数...]")
	}

	ch, err := selectChannel(&launchOptions{})
	if err != nil {
		return err
	}
	if ch == nil {
		return fmt.Errorf("没有可用的代理通道")
	}

	fmt.Printf("通道 %s\n", ch.String())
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = launcher.BuildEnv(*ch, nil)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// terminateIDE 终止正在运行的桌面版实例。
//
// 容忍"进程已经不在"的情况：taskkill 对找不到的进程返回 exit 128，
// pkill 对没有匹配进程返回 exit 1。这两种都不该算失败。
func terminateIDE(inst launcher.RunningInstance) error {
	if runtime.GOOS == "windows" {
		err := exec.Command("taskkill", "/F", "/IM", "Antigravity.exe").Run()
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				// 128 = 没有找到匹配进程。
				if exitErr.ExitCode() == 128 {
					return nil
				}
			}
			return err
		}
		return nil
	}
	err := exec.Command("pkill", "-f", "Antigravity.app").Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil
		}
		return err
	}
	return nil
}

// decodeJSON 是 json.Unmarshal 的薄封装，便于以后换实现。
func decodeJSON(b []byte, v any) error { return json.Unmarshal(b, v) }

// encodeJSON 是 json.MarshalIndent 的薄封装。
func encodeJSON(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }

// contextUnused 保留 context 引用，避免导入被裁掉。
var _ = context.Background
