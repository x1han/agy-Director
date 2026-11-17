package shellenv

import (
	"io"
	"strings"
	"testing"
	"time"
)

// inj 是测试用的注入基准。
func inj() Injection {
	return Injection{
		HTTPProxy:  "http://127.0.0.1:10808",
		HTTPSProxy: "http://127.0.0.1:10808",
		NoProxy:    "localhost,127.0.0.1,::1",
	}
}

// TestClassify 验证登录 shell 冲突的分类规则。
//
// 背景（已实证）：
//   - Go 的 x/net/http/httpproxy 用 getEnvAny("HTTPS_PROXY", "https_proxy")，
//     **大写优先**。
//   - Antigravity 的 dist/loginShellEnv.js spawn 登录 shell 时带着
//     `{ ...process.env, ...INJECTED_ENV }`，所以登录 shell **继承**了
//     我们注入的值；rc 文件没有再 export 同名变量时，loginShellEnv 里的
//     代理值就是我们自己的值。
//   - `{ ...process.env, ...loginShellEnv }` 是**逐键**覆盖。
//
// 所以只有"值不同"才是真冲突，而且只有大写名不同才致命。
func TestClassify(t *testing.T) {
	tests := []struct {
		name       string
		shellEnv   string
		wantFatal  []string // 致命冲突的变量名
		wantWarn   []string // 警告级冲突的变量名
		wantNoConf []string // 不应出现的变量名
	}{
		{
			name:      "shell 回显了我们注入的值 → 不是冲突",
			shellEnv:  "HTTPS_PROXY=http://127.0.0.1:10808\nHTTP_PROXY=http://127.0.0.1:10808\nNO_PROXY=localhost,127.0.0.1,::1\n",
			wantFatal: nil,
			wantWarn:  nil,
		},
		{
			name:      "大写 HTTPS_PROXY 被改 → 致命",
			shellEnv:  "HTTPS_PROXY=http://127.0.0.1:7890\n",
			wantFatal: []string{"HTTPS_PROXY"},
		},
		{
			name:      "大写 HTTP_PROXY 被改 → 致命",
			shellEnv:  "HTTP_PROXY=http://127.0.0.1:7890\n",
			wantFatal: []string{"HTTP_PROXY"},
		},
		{
			name:      "大写 NO_PROXY 被改 → 致命",
			shellEnv:  "NO_PROXY=example.com\n",
			wantFatal: []string{"NO_PROXY"},
		},
		{
			// 大写仍在 ours → httpproxy 取大写 → Antigravity 不受影响。
			name:      "只有小写被改 → 警告",
			shellEnv:  "https_proxy=http://127.0.0.1:7890\n",
			wantFatal: nil,
			wantWarn:  []string{"https_proxy"},
		},
		{
			name:      "大小写都被改 → 致命（大写那条）+ 警告（小写那条）",
			shellEnv:  "HTTPS_PROXY=http://127.0.0.1:7890\nhttps_proxy=http://127.0.0.1:7890\n",
			wantFatal: []string{"HTTPS_PROXY"},
			wantWarn:  []string{"https_proxy"},
		},
		{
			// 末尾斜杠是同一个代理，不算冲突。
			name:      "末尾斜杠差异不算冲突",
			shellEnv:  "HTTPS_PROXY=http://127.0.0.1:10808/\n",
			wantFatal: nil,
			wantWarn:  nil,
		},
		{
			name:      "shell 里没有代理变量 → 不是冲突",
			shellEnv:  "PATH=/usr/bin\nHOME=/root\n",
			wantFatal: nil,
			wantWarn:  nil,
		},
		{
			// ALL_PROXY 不在 httpproxy 的读取范围里 → 只警告。
			name:      "ALL_PROXY 被改 → 警告",
			shellEnv:  "ALL_PROXY=socks5://127.0.0.1:1081\n",
			wantFatal: nil,
			wantWarn:  []string{"ALL_PROXY"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conflicts := classify(tt.shellEnv, inj())

			fatal := map[string]bool{}
			warn := map[string]bool{}
			for _, c := range conflicts {
				if c.Severity == SeverityFatal {
					fatal[c.Name] = true
				} else {
					warn[c.Name] = true
				}
			}

			for _, want := range tt.wantFatal {
				if !fatal[want] {
					t.Errorf("缺少致命冲突 %q，实际: %+v", want, conflicts)
				}
			}
			for _, want := range tt.wantWarn {
				if !warn[want] {
					t.Errorf("缺少警告冲突 %q，实际: %+v", want, conflicts)
				}
			}
			if len(tt.wantFatal) == 0 && len(conflicts) > 0 {
				// 无致命时也不该有警告之外的东西；逐条确认严重度。
				for _, c := range conflicts {
					if c.Severity != SeverityWarning {
						t.Errorf("不该出现的冲突 %q（%v）", c.Name, c.Severity)
					}
				}
			}
			for _, no := range tt.wantNoConf {
				if fatal[no] || warn[no] {
					t.Errorf("不该出现冲突 %q", no)
				}
			}
		})
	}
}

// TestClassifySocks5AllProxy 确认 socks5 通道下 ALL_PROXY 与其它变量同值时不是冲突。
func TestClassifySocks5AllProxy(t *testing.T) {
	inj := Injection{
		HTTPProxy:  "socks5://127.0.0.1:10808",
		HTTPSProxy: "socks5://127.0.0.1:10808",
		AllProxy:   "socks5://127.0.0.1:10808",
		NoProxy:    "localhost,127.0.0.1,::1",
	}
	shellEnv := "ALL_PROXY=socks5://127.0.0.1:10808\nall_proxy=socks5://127.0.0.1:10808\n"

	if got := classify(shellEnv, inj); len(got) != 0 {
		t.Errorf("同值不该报冲突，实际: %+v", got)
	}
}

// TestFatal 只挑出致命的那些。
func TestFatal(t *testing.T) {
	conflicts := []Conflict{
		{Name: "https_proxy", Severity: SeverityWarning},
		{Name: "HTTPS_PROXY", Severity: SeverityFatal},
		{Name: "HTTP_PROXY", Severity: SeverityFatal},
	}
	r := Result{Conflicts: conflicts}
	got := r.Fatal()
	if len(got) != 2 {
		t.Fatalf("Fatal 数量 = %d，want 2", len(got))
	}
	for _, c := range got {
		if c.Severity != SeverityFatal {
			t.Errorf("Fatal 里混进了 %q", c.Name)
		}
	}
}

// envValue 从环境切片里取某个变量的值。
func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, prefix); ok {
			return v, true
		}
	}
	return "", false
}

// TestBuildProbeEnv 验证探测用 shell 的环境。
//
// ⚠️ 这一段是"预检不失真"的关键：
//
//  1. 注入的代理变量（大小写两种）必须在 cmd.Env 里，
//     否则用户 .bashrc 里 `export HTTP_PROXY=${HTTP_PROXY:-7890}`
//     会让预检读到 7890 —— 而真启动时父进程是有注入值的，
//     那一行根本不会取默认值。这是**误报致命冲突**的来源。
//
//  2. Antigravity 的 LOGIN_SHELL_INJECTED_ENV 四个变量必须在，
//     否则用户的 .zshrc 可能走一条完全不同的初始化分支。
//
//  3. ALL_PROXY 只在本次会注入它时才设；否则必须**移除**
//     （与 launcher.BuildEnv 一致）。
//
//  4. **空值不写进去**：Injection 字段为空时必须保留继承来的值。
//     写空值会把"有变量但为空"变成"未设置"，于是 `${HTTP_PROXY:-默认值}`
//     照样生效 —— 正好制造要避免的误报。
func TestBuildProbeEnv(t *testing.T) {
	t.Run("注入的代理变量大小写都带上", func(t *testing.T) {
		env := buildProbeEnv(Injection{
			HTTPProxy:  "http://127.0.0.1:10808",
			HTTPSProxy: "http://127.0.0.1:10808",
			NoProxy:    "localhost,127.0.0.1,::1",
		})

		for _, key := range []string{
			"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy",
			"NO_PROXY", "no_proxy",
		} {
			v, ok := envValue(env, key)
			if !ok {
				t.Errorf("缺少 %s", key)
				continue
			}
			if v == "" {
				t.Errorf("%s 是空值", key)
			}
		}
		if v, _ := envValue(env, "HTTPS_PROXY"); v != "http://127.0.0.1:10808" {
			t.Errorf("HTTPS_PROXY = %q", v)
		}
	})

	t.Run("Antigravity 的四个注入变量都在", func(t *testing.T) {
		env := buildProbeEnv(Injection{HTTPProxy: "http://127.0.0.1:10808"})

		for key, want := range AntigravityInjectedEnv {
			v, ok := envValue(env, key)
			if !ok {
				t.Errorf("缺少 %s", key)
				continue
			}
			if v != want {
				t.Errorf("%s = %q，want %q", key, v, want)
			}
		}
	})

	t.Run("http 通道：ALL_PROXY 被移除", func(t *testing.T) {
		t.Setenv("ALL_PROXY", "socks5://127.0.0.1:1081")
		t.Setenv("all_proxy", "socks5://127.0.0.1:1081")

		env := buildProbeEnv(Injection{HTTPProxy: "http://127.0.0.1:10808"})

		if _, ok := envValue(env, "ALL_PROXY"); ok {
			t.Error("ALL_PROXY 应该被移除")
		}
		if _, ok := envValue(env, "all_proxy"); ok {
			t.Error("all_proxy 应该被移除")
		}
	})

	t.Run("socks5 通道：ALL_PROXY 被注入", func(t *testing.T) {
		env := buildProbeEnv(Injection{
			HTTPProxy:  "socks5://127.0.0.1:10808",
			HTTPSProxy: "socks5://127.0.0.1:10808",
			AllProxy:   "socks5://127.0.0.1:10808",
		})

		if v, ok := envValue(env, "ALL_PROXY"); !ok || v != "socks5://127.0.0.1:10808" {
			t.Errorf("ALL_PROXY = %q (存在=%v)", v, ok)
		}
		if v, ok := envValue(env, "all_proxy"); !ok || v != "socks5://127.0.0.1:10808" {
			t.Errorf("all_proxy = %q (存在=%v)", v, ok)
		}
	})

	t.Run("空注入值不覆盖继承来的值", func(t *testing.T) {
		t.Setenv("HTTP_PROXY", "http://inherited:1080")

		env := buildProbeEnv(Injection{HTTPSProxy: "http://127.0.0.1:10808"})

		if v, _ := envValue(env, "HTTP_PROXY"); v != "http://inherited:1080" {
			t.Errorf("HTTP_PROXY = %q，want 继承来的值", v)
		}
	})
}

// TestResultFatal 验证 Result.Fatal 的行为。
func TestResultFatal(t *testing.T) {
	r := Result{Conflicts: []Conflict{
		{Name: "https_proxy", Severity: SeverityWarning},
		{Name: "HTTPS_PROXY", Severity: SeverityFatal},
	}}
	if got := r.Fatal(); len(got) != 1 || got[0].Name != "HTTPS_PROXY" {
		t.Errorf("Fatal = %+v", got)
	}
}

// TestExtractFrame 验证分隔符取帧。
//
// 为什么要取帧而不是直接用裸 env 的输出：
// cmd.Output() 要等 stdout EOF。用户的 rc 文件起了个后台任务、
// 它持有 stdout 不退出时，预检就会一直等到超时。
// Antigravity 的做法是读到收尾分隔符就停（dist/loginShellEnv.js
// 的 parseShellEnvOutput），这里保持一致。
func TestExtractFrame(t *testing.T) {
	d := probeDelimiter

	tests := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{
			// 帧结束于收尾分隔符的**行首**，所以最后那行换行
			// 属于分隔符那一行，不在帧内。
			name: "标准的一帧",
			in:   d + "\nHTTPS_PROXY=http://127.0.0.1:10808\nNO_PROXY=localhost\n" + d,
			want: "\nHTTPS_PROXY=http://127.0.0.1:10808\nNO_PROXY=localhost",
			ok:   true,
		},
		{
			// 收尾分隔符必须在**行首**，否则 rc 文件里的随机输出
			// 可能凑出假的结束位置（Antigravity 的同一规则）。
			name: "收尾分隔符不在行首 → 不算结束",
			in:   d + "\nHTTPS_PROXY=x\nfoo" + d + "\n",
			ok:   false,
		},
		{
			name: "只有起始分隔符 → 帧还没闭合",
			in:   d + "\nHTTPS_PROXY=x\n",
			ok:   false,
		},
		{
			name: "完全没有分隔符",
			in:   "HTTPS_PROXY=x\n",
			ok:   false,
		},
		{
			// dash 会把上一条命令的最后一个参数记成 `_=<分隔符>`，
			// 所以分隔符可能出现两次；帧取的是第一个到行首第二个之间。
			name: "分隔符出现两次",
			in:   d + "\nHTTPS_PROXY=x\n" + d + "\n",
			want: "\nHTTPS_PROXY=x",
			ok:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := extractFrame(tt.in)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v (got %q)", ok, tt.ok, got)
			}
			if ok && got != tt.want {
				t.Errorf("frame = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestReadFramedEnv 验证读帧在**不等 EOF** 的情况下就能返回。
//
// 这是 P2 的核心：rc 文件留了个后台任务持有 stdout，
// 若等 EOF 就会一路等到超时。
func TestReadFramedEnv(t *testing.T) {
	d := probeDelimiter
	// 帧后面还跟着"后台任务"的输出，且 reader **永不返回 EOF**。
	pr, pw := io.Pipe()
	go func() {
		pw.Write([]byte(d + "\nHTTPS_PROXY=http://127.0.0.1:10808\n" + d))
		// 故意不 Close，也不写更多 —— 模拟持有 stdout 的后台任务。
	}()

	got, err := readFramedEnv(pr, 2*time.Second)
	if err != nil {
		t.Fatalf("err = %v（不该等 EOF）", err)
	}
	if !strings.Contains(got, "HTTPS_PROXY=http://127.0.0.1:10808") {
		t.Errorf("帧内容不对: %q", got)
	}
}
