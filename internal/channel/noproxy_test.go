package channel

import (
	"strings"
	"testing"
)

// hasEntry 判断逗号分隔的列表里是否有某一项。
func hasEntry(list, entry string) bool {
	for _, part := range strings.Split(list, ",") {
		if strings.TrimSpace(part) == entry {
			return true
		}
	}
	return false
}

// TestMergeNoProxy 验证大写与小写 NO_PROXY 的合并规则。
//
// ⚠️ 测的是 MergeNoProxy（纯函数）而不是 EffectiveNoProxy
// （读环境变量）：Windows 上 os.Getenv 大小写不敏感，
// NO_PROXY 与 no_proxy 是**同一个**环境条目，
// 在这台机器上根本构造不出"两个都设"的场景。
//
// 真实的 Unix 上它们是两个独立条目：用户的 .bashrc 里常写小写
// `export no_proxy=...`，系统级 /etc/environment 里可能写大写。
// 只读大写会把用户的 bypass 列表整段丢掉 —— 那等于让用户的
// 内网服务全部被代理。
func TestMergeNoProxy(t *testing.T) {
	tests := []struct {
		name  string
		upper string
		lower string
		want  string
	}{
		{
			name:  "都没设",
			upper: "", lower: "",
			want: "",
		},
		{
			name:  "只设了大写",
			upper: "api.example.com", lower: "",
			want: "api.example.com",
		},
		{
			name:  "只设了小写",
			upper: "", lower: "api.example.com",
			want: "api.example.com",
		},
		{
			// 两个都设时都并进来（大写在前）。
			// 大写决定 Antigravity 的实际行为（httpproxy 大写优先），
			// 但把小写也带上不会让本该走代理的流量变成绕过代理。
			name:  "两个都设",
			upper: "api.example.com", lower: "internal.example.com",
			want: "api.example.com,internal.example.com",
		},
		{
			name:  "两个都设且内容相同",
			upper: "api.example.com", lower: "api.example.com",
			want: "api.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MergeNoProxy(tt.upper, tt.lower)
			if got != tt.want {
				t.Errorf("MergeNoProxy(%q, %q) = %q, want %q", tt.upper, tt.lower, got, tt.want)
			}
		})
	}
}

// TestEffectiveNoProxy 验证 EffectiveNoProxy 的整体行为。
//
// 这里只测在当前平台能构造的场景。
//
// ⚠️ **只调一次** t.Setenv：Windows 上 os.Getenv 大小写不敏感，
// NO_PROXY 与 no_proxy 是同一个环境条目，第二次 Setenv 会把
// 第一个覆盖掉（设成 "" 就等于清空）。
// "两个都设"的场景由 TestMergeNoProxy（纯函数）覆盖。
func TestEffectiveNoProxy(t *testing.T) {
	t.Run("回环项恒在", func(t *testing.T) {
		t.Setenv("NO_PROXY", "")

		got := EffectiveNoProxy()
		for _, loop := range []string{"localhost", "127.0.0.1", "::1"} {
			if !hasEntry(got, loop) {
				t.Errorf("缺少回环项 %q，实际: %q", loop, got)
			}
		}
	})

	t.Run("宿主机已有的值保留", func(t *testing.T) {
		t.Setenv("NO_PROXY", "api.example.com")

		got := EffectiveNoProxy()
		if !hasEntry(got, "api.example.com") {
			t.Errorf("宿主机已有的值丢了，实际: %q", got)
		}
		if !hasEntry(got, "localhost") {
			t.Errorf("缺少回环项，实际: %q", got)
		}
	})
}

// TestHostNoProxy 验证原始值读取（不合并回环项）。
//
// doctor 用它当"和宿主机现状比对"的基准：合并过的值会把
// 回环项算成差异，产生假冲突。
//
// 同样只调一次 t.Setenv（理由见 TestEffectiveNoProxy）。
func TestHostNoProxy(t *testing.T) {
	t.Run("设了大写", func(t *testing.T) {
		t.Setenv("NO_PROXY", "api.example.com")
		if got := HostNoProxy(); got != "api.example.com" {
			t.Errorf("HostNoProxy() = %q", got)
		}
	})

	t.Run("没设", func(t *testing.T) {
		t.Setenv("NO_PROXY", "")
		if got := HostNoProxy(); got != "" {
			t.Errorf("HostNoProxy() = %q, want 空", got)
		}
	})

	t.Run("不合并回环项", func(t *testing.T) {
		t.Setenv("NO_PROXY", "api.example.com")
		got := HostNoProxy()
		if hasEntry(got, "localhost") {
			t.Errorf("HostNoProxy 不该合并回环项，实际: %q", got)
		}
	})
}
