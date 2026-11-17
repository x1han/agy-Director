package discovery

import (
	"path/filepath"
	"testing"
)

// TestReadAsarPackageVersion 在本机真实 Antigravity 安装上验证 asar 解析。
// 没装 Antigravity 时跳过。
func TestReadAsarPackageVersion(t *testing.T) {
	target, ok := FindTarget(TargetIDE)
	if !ok {
		t.Skip("Antigravity 桌面版未安装，跳过")
	}
	v := readAsarPackageVersion(filepath.Dir(target.Path))
	if v == "" {
		t.Fatalf("未能从 %s 读出 asar 版本", target.Path)
	}
	t.Logf("asar version = %s", v)
}

// TestFindTargetIDE 确认桌面版能被定位。
func TestFindTargetIDE(t *testing.T) {
	target, ok := FindTarget(TargetIDE)
	if !ok {
		t.Skip("Antigravity 桌面版未安装，跳过")
	}
	t.Logf("found: %s (version=%s)", target.Path, target.Version)
}
