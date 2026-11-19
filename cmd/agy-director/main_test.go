package main

import (
	"strings"
	"testing"
)

// TestNormalizeArgs 验证无参数时的默认行为。
//
// 背景：`agy-Director.exe` 是可以被双击的（桌面快捷方式），
// 无参数时如果只打印帮助然后 exit 2，用户看到的就是"一闪而过"。
// 所以无参数等价于 tui —— 弹出终端、渲染 TUI、确认后启动。
func TestNormalizeArgs(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "无参数 → tui",
			in:   nil,
			want: []string{"tui"},
		},
		{
			name: "显式 launch 不变",
			in:   []string{"launch", "--yes"},
			want: []string{"launch", "--yes"},
		},
		{
			name: "显式 tui 不变",
			in:   []string{"tui", "--verbose"},
			want: []string{"tui", "--verbose"},
		},
		{
			name: "help 保持不变",
			in:   []string{"help"},
			want: []string{"help"},
		},
		{
			name: "doctor 保持不变",
			in:   []string{"doctor", "--processes"},
			want: []string{"doctor", "--processes"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeArgs(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("normalizeArgs(%v) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("normalizeArgs(%v) = %v, want %v", tt.in, got, tt.want)
				}
			}
		})
	}
}

// TestUsageMentionsTUIDefault 确认帮助文本说明了默认行为。
func TestUsageMentionsTUIDefault(t *testing.T) {
	if !strings.Contains(usage, "终端图形界面") {
		t.Errorf("帮助文本没说明无参数时走 TUI")
	}
}

// TestParseTUIArgs 验证 tui 子命令的参数解析。
func TestParseTUIArgs(t *testing.T) {
	t.Run("空参数", func(t *testing.T) {
		verboseMode, noWait = false, false
		if err := parseTUIArgs(nil); err != nil {
			t.Fatalf("空参数不应报错: %v", err)
		}
		if verboseMode || noWait {
			t.Error("空参数不应打开任何开关")
		}
	})

	t.Run("verbose", func(t *testing.T) {
		verboseMode, noWait = false, false
		if err := parseTUIArgs([]string{"--verbose"}); err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		if !verboseMode {
			t.Error("--verbose 应打开 verbose")
		}
		if noWait {
			t.Error("--verbose 不应打开 no-wait")
		}
	})

	t.Run("no-wait", func(t *testing.T) {
		verboseMode, noWait = false, false
		if err := parseTUIArgs([]string{"--no-wait"}); err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		if !noWait {
			t.Error("--no-wait 应打开 no-wait")
		}
	})

	t.Run("未知选项", func(t *testing.T) {
		verboseMode, noWait = false, false
		if err := parseTUIArgs([]string{"--bogus"}); err == nil {
			t.Error("未知选项应当报错")
		}
	})

	t.Run("多余参数", func(t *testing.T) {
		verboseMode, noWait = false, false
		if err := parseTUIArgs([]string{"extra"}); err == nil {
			t.Error("多余参数应当报错")
		}
	})
}
