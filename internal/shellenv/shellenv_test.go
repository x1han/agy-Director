package shellenv

import "testing"

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
	got := Fatal(conflicts)
	if len(got) != 2 {
		t.Fatalf("Fatal 数量 = %d，want 2", len(got))
	}
	for _, c := range got {
		if c.Severity != SeverityFatal {
			t.Errorf("Fatal 里混进了 %q", c.Name)
		}
	}
}
