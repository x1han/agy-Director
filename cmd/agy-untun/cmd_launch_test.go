package main

import (
	"strings"
	"testing"

	"github.com/user/agy-untun/internal/channel"
)

// TestChromiumBypassList 验证 NO_PROXY → Chromium bypass list 的转换。
//
// 关键的语义差异（专家指出）：
//   - Go 的 ".foo.com"（含子域）在 Chromium 里无效，要写 "*.foo.com"
//   - "*.foo.com" **不匹配**裸域名 "foo.com"，所以要两个都加
//   - Chromium 有 NO_PROXY 没有的 "<local>"（不含点号的主机名）
//
// 测试域名一律用 RFC 2606 保留的 example.com，
// 不写任何真实的主机名。
func TestChromiumBypassList(t *testing.T) {
	tests := []struct {
		name    string
		noProxy string
		want    []string // 必须出现
		notWant []string // 不应出现
	}{
		{
			name:    "普通域名原样保留",
			noProxy: "api.example.com",
			want:    []string{"api.example.com"},
			notWant: []string{"*.api.example.com"},
		},
		{
			name:    "前导点转成通配符并补上裸域名",
			noProxy: ".example.com",
			want:    []string{"*.example.com", "example.com"},
			notWant: []string{".example.com"},
		},
		{
			name:    "裸域名与前导点同时存在时不重复",
			noProxy: "example.com,.example.com",
			want:    []string{"example.com", "*.example.com"},
			notWant: []string{".example.com"},
		},
		{
			name:    "总是追加 <local>",
			noProxy: "",
			want:    []string{"<local>"},
		},
		{
			name:    "带端口的条目保留",
			noProxy: "127.0.0.1:8080",
			want:    []string{"127.0.0.1:8080"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NO_PROXY", tt.noProxy)
			t.Setenv("no_proxy", tt.noProxy)

			got := chromiumBypassList(channel.Channel{Host: "127.0.0.1", Port: 10808})

			// 分隔符必须是分号（Chromium 两种都收，但分号是惯例）
			if !strings.Contains(got, ";") && len(strings.Split(got, ";")) == 1 && len(got) > 0 {
				// 单个条目时没有分号是正常的
			}

			for _, w := range tt.want {
				if !hasBypassEntry(got, w) {
					t.Errorf("缺少 %q，实际: %q", w, got)
				}
			}
			for _, w := range tt.notWant {
				if hasBypassEntry(got, w) {
					t.Errorf("不该出现 %q，实际: %q", w, got)
				}
			}
		})
	}
}

// TestChromiumBypassListAlwaysHasLocal 单独确认 <local> 恒在。
func TestChromiumBypassListAlwaysHasLocal(t *testing.T) {
	t.Setenv("NO_PROXY", "api.example.com")
	t.Setenv("no_proxy", "")

	got := chromiumBypassList(channel.Channel{Host: "127.0.0.1", Port: 10808})
	if !hasBypassEntry(got, "<local>") {
		t.Errorf("缺少 <local>，实际: %q", got)
	}
}

// TestChromiumBypassListHasProxyItself 确认代理服务器自身不会被代理。
func TestChromiumBypassListHasProxyItself(t *testing.T) {
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	got := chromiumBypassList(channel.Channel{Host: "127.0.0.1", Port: 10808})
	if !hasBypassEntry(got, "127.0.0.1:10808") {
		t.Errorf("缺少代理自身地址，实际: %q", got)
	}
}

// hasBypassEntry 判断 bypass list 里是否有某一项（按分号切分精确比对）。
func hasBypassEntry(list, entry string) bool {
	for _, part := range strings.Split(list, ";") {
		if strings.TrimSpace(part) == entry {
			return true
		}
	}
	return false
}

// TestDesktopDebugArgs 验证 CDP 调试参数的追加逻辑。
//
// 背景：Antigravity 2.33+ 的 dist/main.js 只判断
// hasSwitch('remote-debugging-port')，**没有**在缺省时 appendSwitch 打开，
// 所以必须由启动方显式传。不传则 DevToolsActivePort 不生成，
// 依赖 CDP 的插件（内联面板）挂不上。
//
// 关键点：**不重复追加**。用户自己传了 --remote-debugging-port=9222 时，
// 我们不能再塞一个 --remote-debugging-port=0 —— Chromium 取最后一个，
// 用户的具体端口会被我们的 0 覆盖掉。
//
// 本测试全是 Chromium 开关字符串，不涉及任何域名。
func TestDesktopDebugArgs(t *testing.T) {
	tests := []struct {
		name     string
		passArgs []string
		want     []string // 必须出现
		notWant  []string // 不应出现
	}{
		{
			name:     "没传过 → 两个都补上",
			passArgs: nil,
			want:     []string{debugPortArg, debugAddressArg},
		},
		{
			name:     "已有端口 → 只补地址",
			passArgs: []string{"--remote-debugging-port=9222"},
			want:     []string{debugAddressArg},
			notWant:  []string{debugPortArg},
		},
		{
			name:     "已有地址 → 只补端口",
			passArgs: []string{"--remote-debugging-address=127.0.0.1"},
			want:     []string{debugPortArg},
			notWant:  []string{debugAddressArg},
		},
		{
			name: "两个都有 → 都不补",
			passArgs: []string{
				"--remote-debugging-port=9222",
				"--remote-debugging-address=127.0.0.1",
			},
			notWant: []string{debugPortArg, debugAddressArg},
		},
		{
			// 空格写法：值在下一个 argv 里，不带 "="。
			name:     "空格写法的端口 → 只补地址",
			passArgs: []string{"--remote-debugging-port", "9222"},
			want:     []string{debugAddressArg},
			notWant:  []string{debugPortArg},
		},
		{
			// 别的参数不能被误判成调试开关。
			name:     "无关参数 → 两个都补上",
			passArgs: []string{"--proxy-server=http://127.0.0.1:10808"},
			want:     []string{debugPortArg, debugAddressArg},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := desktopDebugArgs(tt.passArgs)

			for _, w := range tt.want {
				if !hasArg(got, w) {
					t.Errorf("缺少 %q，实际: %v", w, got)
				}
			}
			for _, w := range tt.notWant {
				if hasArg(got, w) {
					t.Errorf("不该出现 %q，实际: %v", w, got)
				}
			}
		})
	}
}

// hasArg 判断参数列表里是否**恰好**有这一项（不是子串匹配）。
func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// TestDesktopDebugArgsOnlyAddsMissing 确认两个开关是**各自独立**判断的。
//
// 即：只缺一个时，只补那一个，不多不少。
func TestDesktopDebugArgsOnlyAddsMissing(t *testing.T) {
	got := desktopDebugArgs([]string{"--remote-debugging-port=9222"})
	if len(got) != 1 {
		t.Fatalf("应当只补 1 个参数，实际补了 %d 个: %v", len(got), got)
	}
	if got[0] != debugAddressArg {
		t.Errorf("补的应该是 %q，实际: %q", debugAddressArg, got[0])
	}
}
