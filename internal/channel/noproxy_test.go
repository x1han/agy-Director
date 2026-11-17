package channel

import (
	"runtime"
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

// TestSelectNoProxy 验证 NO_PROXY 的取值规则。
//
// 语义必须与 Go 的 x/net/http/httpproxy **完全一致**：
//
//	NoProxy: getEnvAny("NO_PROXY", "no_proxy")
//
// 取第一个非空的，**绝不拼接**。
//
// ⚠️ 拼接是错的：NO_PROXY 是**绕过代理的白名单**，
// 往里加条目 = 让原本走代理的流量变成直连。
//
// ⚠️ 测的是 SelectNoProxy（纯函数）而不是 EffectiveNoProxy
// （读环境变量）：Windows 上 os.Getenv 大小写不敏感，
// NO_PROXY 与 no_proxy 是**同一个**环境条目，
// 在这台机器上根本构造不出"两个都设"的场景。
// "两个都设"的语义由这个纯函数覆盖，与平台无关。
func TestSelectNoProxy(t *testing.T) {
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
			// 两个都设时**只取大写**，绝不拼接。
			name:  "两个都设 → 只取大写",
			upper: "api.example.com", lower: "internal.example.com",
			want: "api.example.com",
		},
		{
			name:  "两个都设且内容相同",
			upper: "api.example.com", lower: "api.example.com",
			want: "api.example.com",
		},
		{
			// 大写是空字符串时（不是"未设置"）也要落到小写。
			name:  "大写为空串 → 取小写",
			upper: "", lower: "internal.example.com",
			want: "internal.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SelectNoProxy(tt.upper, tt.lower)
			if got != tt.want {
				t.Errorf("SelectNoProxy(%q, %q) = %q, want %q", tt.upper, tt.lower, got, tt.want)
			}
		})
	}
}

// setNoProxy 同时设置 NO_PROXY 与 no_proxy。
//
// ⚠️ **平台差异（这是本文件最容易踩的坑）**：
//
//   - **Windows**：os.Getenv 大小写不敏感，NO_PROXY 与 no_proxy
//     是**同一个**环境条目。设两次等于设一次。
//   - **Unix**：两个是**互相独立**的条目。只设大写，小写的旧值
//     （比如宿主机 rc 文件里 export 的）会**原样留着**。
//
// 所以：
//   - Windows 上只设一次（大写），避免第二次把第一个覆盖掉；
//   - Unix 上**两个都要设**，否则测出来的是宿主机遗留值，
//     不是我们想要的状态。
//
// 这个 bug 真的踩过：在 WSL 上跑时，宿主的 no_proxy 有值，
// "没设"用例就挂了。
func setNoProxy(t *testing.T, upper, lower string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		// 同一个条目：大写优先，所以非空就取大写。
		v := upper
		if v == "" {
			v = lower
		}
		t.Setenv("NO_PROXY", v)
		return
	}
	t.Setenv("NO_PROXY", upper)
	t.Setenv("no_proxy", lower)
}

// TestEffectiveNoProxy 验证 EffectiveNoProxy 的整体行为。
func TestEffectiveNoProxy(t *testing.T) {
	t.Run("回环项恒在", func(t *testing.T) {
		setNoProxy(t, "", "")

		got := EffectiveNoProxy()
		for _, loop := range []string{"localhost", "127.0.0.1", "::1"} {
			if !hasEntry(got, loop) {
				t.Errorf("缺少回环项 %q，实际: %q", loop, got)
			}
		}
	})

	t.Run("宿主机已有的值保留", func(t *testing.T) {
		setNoProxy(t, "api.example.com", "")

		got := EffectiveNoProxy()
		if !hasEntry(got, "api.example.com") {
			t.Errorf("宿主机已有的值丢了，实际: %q", got)
		}
		if !hasEntry(got, "localhost") {
			t.Errorf("缺少回环项，实际: %q", got)
		}
	})

	// ⚠️ 只有 Unix 上才构造得出来的场景：
	// 用户 rc 文件里写的是**小写** no_proxy。
	// 这正是"不能只看大写"的理由 —— 只读大写会把用户的
	// 内网 bypass 列表整段丢掉，等于让内网服务全部被代理。
	if runtime.GOOS != "windows" {
		t.Run("只有小写设了 → 小写的值不能丢", func(t *testing.T) {
			setNoProxy(t, "", "internal.example.com")

			got := EffectiveNoProxy()
			if !hasEntry(got, "internal.example.com") {
				t.Errorf("小写的值丢了，实际: %q", got)
			}
		})
	}
}

// TestHostNoProxy 验证原始值读取（不合并回环项）。
//
// doctor 用它当"和宿主机现状比对"的基准：合并过的值会把
// 回环项算成差异，产生假冲突。
func TestHostNoProxy(t *testing.T) {
	t.Run("设了大写", func(t *testing.T) {
		setNoProxy(t, "api.example.com", "")

		if got := HostNoProxy(); got != "api.example.com" {
			t.Errorf("HostNoProxy() = %q", got)
		}
	})

	t.Run("没设", func(t *testing.T) {
		setNoProxy(t, "", "")

		if got := HostNoProxy(); got != "" {
			t.Errorf("HostNoProxy() = %q, want 空", got)
		}
	})

	t.Run("不合并回环项", func(t *testing.T) {
		setNoProxy(t, "api.example.com", "")

		got := HostNoProxy()
		if hasEntry(got, "localhost") {
			t.Errorf("HostNoProxy 不该合并回环项，实际: %q", got)
		}
	})
}
