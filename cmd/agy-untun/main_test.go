package main

import (
	"strings"
	"testing"
)

// TestNormalizeArgs 验证无参数时的默认行为。
//
// 背景：`agy-untun.exe` 是可以被双击的（桌面快捷方式），
// 无参数时如果只打印帮助然后 exit 2，用户看到的就是"一闪而过"。
// 所以无参数等价于 launch。
func TestNormalizeArgs(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "无参数 → launch",
			in:   nil,
			want: []string{"launch"},
		},
		{
			name: "显式 launch 不变",
			in:   []string{"launch", "--yes"},
			want: []string{"launch", "--yes"},
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

// TestUsageMentionsLaunchDefault 确认帮助文本说明了默认行为。
func TestUsageMentionsLaunchDefault(t *testing.T) {
	if !strings.Contains(usage, "等价于 agy-untun launch") {
		t.Errorf("帮助文本没说明无参数时的默认行为")
	}
}
