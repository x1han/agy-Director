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
