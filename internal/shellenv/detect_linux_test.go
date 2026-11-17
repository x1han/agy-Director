//go:build linux || darwin

package shellenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFakeShell 写一个假的"登录 shell"，模拟用户的 rc 行为。
//
// 用法：把 fakeShell 设成 $SHELL，跑 Detect。
func writeFakeShell(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fakeshell")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("写假 shell: %v", err)
	}
	return path
}

// TestDetect_BackgroundProcessHoldsStdout 是本包最重要的一条回归测试。
//
// 场景：用户的 rc 文件起了一个后台进程（ssh-agent / gpg-agent /
// 自定义守护），它继承了 stdout 管道写端并一直活着。
//
// 此时：
//   - 假 shell 自己 exit 了；
//   - 但管道写端还开着 → 读端永远等不到 EOF；
//   - 早期版本的 readFramedEnv 把 deadline 检查放在循环顶部，
//     而 Read 是阻塞的 → 卡在 Read 里永远出不来 → **启动器被挂死**。
//
// 这条测试断言 Detect 必须在有界时间内返回（不能挂死）。
func TestDetect_BackgroundProcessHoldsStdout(t *testing.T) {
	// 后台进程：睡很久，持有 stdout。
	// 注意用 `setsid`/子 shell 让它真正脱离并继承管道。
	shell := writeFakeShell(t, `
# 模拟 rc 文件起的后台任务：继承 stdout 并一直活着
( sleep 300 ) &

# 打印我们想要的环境
echo -n "`+probeDelimiter+`"
/usr/bin/env
echo -n "`+probeDelimiter+`"
exit 0
`)

	t.Setenv("SHELL", shell)

	start := time.Now()
	res := Detect(3*time.Second, Injection{
		HTTPProxy:  "http://127.0.0.1:10808",
		HTTPSProxy: "http://127.0.0.1:10808",
		NoProxy:    "localhost,127.0.0.1,::1",
	})
	elapsed := time.Since(start)

	// 3s 超时 + 500ms WaitDelay 收尸余量。
	if max := 6 * time.Second; elapsed > max {
		t.Fatalf("Detect 耗时 %v，超出上界 %v —— 挂死了", elapsed, max)
	}
	t.Logf("Detect 耗时 %v, Inconclusive=%v, 冲突数=%d", elapsed, res.Inconclusive, len(res.Conflicts))

	// 关键是**必须返回**。至于读没读到帧，不作要求：
	// 后台任务先跑，帧完全可能已经读到了。
	if res.Inconclusive {
		t.Logf("没能读到环境（可接受）：%v", res.Err)
	}
}

// TestDetect_NoClosingDelimiter_BackgroundHoldsStdout 是**最狠**的一条：
// 帧永远闭合不上 + 后台进程持有 stdout 写端。
//
// 这才是真正的死锁场景：
//   - 假 shell 只打了起始分隔符，**永远不打收尾分隔符**（模拟
//     rc 文件里某个东西把 stdout 弄脏了 / 命令被中断）；
//   - 后台进程继承了 stdout 写端并一直活着 → 永远等不到 EOF；
//   - 早期版本的 Read 是阻塞的，deadline 检查走不到 → **永久挂死**。
//
// 断言：Detect 必须在 timeout + WaitDelay 的上界内返回，
// 且以"无法确认"（Inconclusive）结束，而不是挂死。
func TestDetect_NoClosingDelimiter_BackgroundHoldsStdout(t *testing.T) {
	shell := writeFakeShell(t, `
# 后台任务：继承 stdout 并一直活着
( sleep 300 ) &

# 只打起始分隔符，**永远不打收尾分隔符**
echo -n "`+probeDelimiter+`"
/usr/bin/env
exit 0
`)
	t.Setenv("SHELL", shell)

	start := time.Now()
	res := Detect(2*time.Second, Injection{
		HTTPProxy:  "http://127.0.0.1:10808",
		HTTPSProxy: "http://127.0.0.1:10808",
		NoProxy:    "localhost,127.0.0.1,::1",
	})
	elapsed := time.Since(start)

	// 2s 超时 + 500ms WaitDelay + 200ms 打断宽限。
	if max := 6 * time.Second; elapsed > max {
		t.Fatalf("Detect 耗时 %v，超出上界 %v —— 挂死了", elapsed, max)
	}
	t.Logf("Detect 耗时 %v, Inconclusive=%v, err=%v", elapsed, res.Inconclusive, res.Err)

	// 帧闭合不上 → 必须报"无法确认"，绝不能报"没有冲突"。
	if !res.Inconclusive {
		t.Errorf("帧没闭合却报告了确定结果，冲突数=%d", len(res.Conflicts))
	}
	if res.Err == nil {
		t.Error("应该有错误说明为什么没能确认")
	}
}

// TestDetect_CleanShell 验证正常情况下 Detect 能读到环境并正确判定冲突。
func TestDetect_CleanShell(t *testing.T) {
	shell := writeFakeShell(t, `
echo -n "`+probeDelimiter+`"
/usr/bin/env
echo -n "`+probeDelimiter+`"
exit 0
`)
	t.Setenv("SHELL", shell)

	inj := Injection{
		HTTPProxy:  "http://127.0.0.1:10808",
		HTTPSProxy: "http://127.0.0.1:10808",
		NoProxy:    "localhost,127.0.0.1,::1",
	}

	res := Detect(10*time.Second, inj)
	if res.Inconclusive {
		t.Fatalf("没能读到登录 shell 环境: %v", res.Err)
	}
	if len(res.Conflicts) != 0 {
		t.Errorf("不该有冲突，实际: %+v", res.Conflicts)
	}
}

// TestDetect_UpperCaseProxyOverridden 验证致命冲突能被检出。
//
// 假 shell 把 HTTPS_PROXY 改成了别的值 → 大写名被改 → 致命。
func TestDetect_UpperCaseProxyOverridden(t *testing.T) {
	shell := writeFakeShell(t, `
export HTTPS_PROXY=http://127.0.0.1:7890
echo -n "`+probeDelimiter+`"
/usr/bin/env
echo -n "`+probeDelimiter+`"
exit 0
`)
	t.Setenv("SHELL", shell)

	res := Detect(10*time.Second, Injection{
		HTTPProxy:  "http://127.0.0.1:10808",
		HTTPSProxy: "http://127.0.0.1:10808",
		NoProxy:    "localhost,127.0.0.1,::1",
	})
	if res.Inconclusive {
		t.Fatalf("没能读到登录 shell 环境: %v", res.Err)
	}

	fatal := res.Fatal()
	if len(fatal) != 1 || fatal[0].Name != "HTTPS_PROXY" {
		t.Fatalf("致命冲突 = %+v，want 一个 HTTPS_PROXY", fatal)
	}
	if !strings.Contains(fatal[0].Reason, "httpproxy") {
		t.Errorf("原因说明不对: %q", fatal[0].Reason)
	}
}
